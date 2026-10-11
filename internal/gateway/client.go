package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"goyard/internal/config"
	"goyard/internal/metrics"
	"goyard/internal/presence"
	"goyard/internal/supervisor"
)

const (
	OpDispatch            = 0
	OpHeartbeat           = 1
	OpIdentify            = 2
	OpPresenceUpdate      = 3
	OpResume              = 6
	OpReconnect           = 7
	OpRequestGuildMembers = 8
	OpInvalidSession      = 9
	OpHello               = 10
	OpHeartbeatAck        = 11

	// GUILDS (1) | GUILD_MEMBERS (2) | GUILD_PRESENCES (256) | GUILD_MESSAGES (512) | DIRECT_MESSAGES (4096) | MESSAGE_CONTENT (32768)
	GatewayIntents = 37635
)

type MessageHandler func(msg map[string]any)

type Client struct {
	cfg           *config.Config
	manager       *presence.Manager
	onMessage     MessageHandler
	gatewayURL    string
	identifyLimit time.Time
	mu            sync.Mutex

	// Session state preserved across resumes
	sessionID        string
	resumeGatewayURL string
	lastSeq          atomic.Int64
}

func NewClient(cfg *config.Config, manager *presence.Manager, onMessage MessageHandler) *Client {
	return &Client{
		cfg:        cfg,
		manager:    manager,
		onMessage:  onMessage,
		gatewayURL: "wss://gateway.discord.gg/?v=10&encoding=json",
	}
}

func (c *Client) SetGatewayURL(u string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gatewayURL = u
}

// Run connects and manages gateway lifecycle. Intended to be run under supervisor.Supervise.
func (c *Client) Run(ctx context.Context) error {
	c.mu.Lock()
	url := c.gatewayURL
	if c.resumeGatewayURL != "" {
		resURL := c.resumeGatewayURL
		if !strings.Contains(resURL, "?") {
			resURL += "/?v=10&encoding=json"
		}
		url = resURL
	}
	c.mu.Unlock()

	slog.Info("gateway connecting", "url", url)
	dialOpts := &websocket.DialOptions{}
	wsConn, _, err := websocket.Dial(ctx, url, dialOpts)
	if err != nil {
		metrics.DefaultMetrics.GatewayConnected.Store(0)
		return fmt.Errorf("gateway dial failed: %w", err)
	}
	defer wsConn.Close(websocket.StatusGoingAway, "client_closing")

	wsConn.SetReadLimit(32 * 1024 * 1024) // 32 MiB

	// Write loop with token bucket (120 events / 60 s => burst of up to 120, 2 tokens/sec)
	writeChan := make(chan []byte, 64)
	writerDone := make(chan struct{})

	supervisor.Go("gw-writer", func() {
		defer close(writerDone)
		tokens := 120
		lastToken := time.Now()

		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-writeChan:
				if !ok {
					return
				}
				// replenish tokens
				now := time.Now()
				elapsed := now.Sub(lastToken)
				added := int(elapsed / (500 * time.Millisecond))
				if added > 0 {
					tokens += added
					if tokens > 120 {
						tokens = 120
					}
					lastToken = now
				}
				if tokens > 0 {
					tokens--
				} else {
					time.Sleep(500 * time.Millisecond)
				}

				writeCtx, writeCancel := context.WithTimeout(ctx, 5*time.Second)
				err := wsConn.Write(writeCtx, websocket.MessageText, data)
				writeCancel()
				if err != nil {
					return
				}
			}
		}
	})

	sendJSON := func(op int, d any) {
		payload := map[string]any{
			"op": op,
			"d":  d,
		}
		data, err := json.Marshal(payload)
		if err == nil {
			select {
			case writeChan <- data:
			default:
				slog.Warn("gateway write channel full")
			}
		}
	}

	heartbeatStop := make(chan struct{})
	var heartbeatOnce sync.Once
	stopHeartbeat := func() {
		heartbeatOnce.Do(func() {
			close(heartbeatStop)
		})
	}
	defer stopHeartbeat()

	var lastAck atomic.Bool
	lastAck.Store(true)

	// Reader loop
	for {
		msgType, data, err := wsConn.Read(ctx)
		if err != nil {
			metrics.DefaultMetrics.GatewayConnected.Store(0)
			closeStatus := websocket.CloseStatus(err)
			slog.Warn("gateway connection closed", "code", closeStatus, "error", err)

			switch closeStatus {
			case 4004:
				slog.Error("invalid BOT_TOKEN provided")
				return supervisor.ErrFatal
			case 4013, 4014:
				slog.Error("disallowed or invalid intents: enable 'Presence Intent' and 'Server Members Intent' in the Discord Developer Portal -> Bot")
				return supervisor.ErrFatal
			case 4007, 4009:
				c.clearSession()
				return errors.New("session invalidated; re-identifying")
			default:
				return fmt.Errorf("gateway connection closed: %w", err)
			}
		}

		if msgType != websocket.MessageText {
			continue
		}

		var envelope struct {
			Op int             `json:"op"`
			S  *int64          `json:"s"`
			T  *string         `json:"t"`
			D  json.RawMessage `json:"d"`
		}

		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.UseNumber()
		if err := dec.Decode(&envelope); err != nil {
			continue
		}

		if envelope.S != nil {
			c.lastSeq.Store(*envelope.S)
		}

		switch envelope.Op {
		case OpHello:
			var helloData struct {
				HeartbeatInterval int64 `json:"heartbeat_interval"`
			}
			_ = json.Unmarshal(envelope.D, &helloData)
			interval := time.Duration(helloData.HeartbeatInterval) * time.Millisecond

			// Start heartbeater
			supervisor.Go("gw-heartbeat", func() {
				// Initial jitter
				jitter := time.Duration(float64(interval) * rand.Float64())
				select {
				case <-heartbeatStop:
					return
				case <-ctx.Done():
					return
				case <-time.After(jitter):
				}

				ticker := time.NewTicker(interval)
				defer ticker.Stop()

				for {
					if !lastAck.Load() {
						slog.Warn("heartbeat ACK not received; closing connection to resume")
						metrics.DefaultMetrics.GatewayReconnects.Add(1)
						_ = wsConn.Close(4000, "stale_heartbeat")
						return
					}
					lastAck.Store(false)

					seq := c.lastSeq.Load()
					var seqVal any = seq
					if seq == 0 {
						seqVal = nil
					}
					sendJSON(OpHeartbeat, seqVal)

					select {
					case <-heartbeatStop:
						return
					case <-ctx.Done():
						return
					case <-ticker.C:
					}
				}
			})

			// Handshake: Resume if session exists, else Identify
			c.mu.Lock()
			hasSession := c.sessionID != "" && c.lastSeq.Load() > 0
			sessID := c.sessionID
			c.mu.Unlock()

			if hasSession {
				slog.Info("resuming gateway session", "session_id", sessID, "seq", c.lastSeq.Load())
				sendJSON(OpResume, map[string]any{
					"token":      c.cfg.BotToken,
					"session_id": sessID,
					"seq":        c.lastSeq.Load(),
				})
			} else {
				// Identify rate limit (at most 1 per 5s)
				c.mu.Lock()
				if time.Now().Before(c.identifyLimit) {
					time.Sleep(time.Until(c.identifyLimit))
				}
				c.identifyLimit = time.Now().Add(5 * time.Second)
				c.mu.Unlock()

				slog.Info("identifying to gateway")
				sendJSON(OpIdentify, map[string]any{
					"token": c.cfg.BotToken,
					"properties": map[string]string{
						"os":      "linux",
						"browser": "lanyard-go",
						"device":  "lanyard-go",
					},
					"presence": map[string]any{
						"since": nil,
						"activities": []map[string]any{
							{
								"name": c.cfg.BotPresence,
								"type": c.cfg.BotPresenceType,
							},
						},
						"status": "online",
						"afk":    false,
					},
					"compress":        false,
					"large_threshold": 250,
					"intents":         GatewayIntents,
				})
			}

		case OpHeartbeatAck:
			lastAck.Store(true)

		case OpHeartbeat:
			// Immediate heartbeat request
			seq := c.lastSeq.Load()
			var seqVal any = seq
			if seq == 0 {
				seqVal = nil
			}
			sendJSON(OpHeartbeat, seqVal)

		case OpReconnect:
			slog.Warn("gateway requested reconnect; closing to resume")
			metrics.DefaultMetrics.GatewayReconnects.Add(1)
			_ = wsConn.Close(4000, "reconnect")
			return errors.New("gateway requested reconnect")

		case OpInvalidSession:
			slog.Warn("gateway session invalid; clearing and re-identifying")
			c.clearSession()
			// Wait 1-5s random before reconnecting
			wait := time.Duration(1000+rand.IntN(4000)) * time.Millisecond
			select {
			case <-time.After(wait):
			case <-ctx.Done():
			}
			_ = wsConn.Close(4000, "invalid_session")
			return errors.New("invalid session")

		case OpDispatch:
			if envelope.T != nil {
				c.handleDispatch(*envelope.T, envelope.D, sendJSON)
			}
		}
	}
}

func (c *Client) clearSession() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessionID = ""
	c.resumeGatewayURL = ""
	c.lastSeq.Store(0)
}

func (c *Client) handleDispatch(t string, rawD json.RawMessage, sendJSON func(op int, d any)) {
	var d map[string]any
	dec := json.NewDecoder(strings.NewReader(string(rawD)))
	dec.UseNumber()
	if err := dec.Decode(&d); err != nil {
		return
	}

	switch t {
	case "READY":
		c.mu.Lock()
		if sid, ok := d["session_id"].(string); ok {
			c.sessionID = sid
		}
		if rURL, ok := d["resume_gateway_url"].(string); ok {
			c.resumeGatewayURL = rURL
		}
		c.mu.Unlock()

		metrics.DefaultMetrics.GatewayConnected.Store(1)
		slog.Info("gateway ready", "session_id", c.sessionID)

	case "GUILD_CREATE":
		guildID, _ := d["id"].(string)
		c.processMembersAndPresences(d)
		if guildID != "" {
			sendJSON(OpRequestGuildMembers, map[string]any{
				"guild_id":  guildID,
				"limit":     0,
				"query":     "",
				"presences": true,
			})
		}

	case "GUILD_MEMBERS_CHUNK":
		c.processMembersAndPresences(d)

	case "PRESENCE_UPDATE":
		metrics.DefaultMetrics.PresenceUpdatesTotal.Add(1)
		if userObj, ok := d["user"].(map[string]any); ok {
			if uid, ok := userObj["id"].(string); ok {
				c.manager.Sync(uid, presence.Patch{DiscordPresence: d}, false)
			}
		}

	case "GUILD_MEMBER_ADD":
		guildID, _ := d["guild_id"].(string)
		if userObj, ok := d["user"].(map[string]any); ok {
			if uid, ok := userObj["id"].(string); ok && guildID != "" {
				sendJSON(OpRequestGuildMembers, map[string]any{
					"guild_id":  guildID,
					"user_ids":  []string{uid},
					"limit":     1,
					"presences": true,
				})
			}
		}

	case "GUILD_MEMBER_UPDATE":
		if userObj, ok := d["user"].(map[string]any); ok {
			if uid, ok := userObj["id"].(string); ok {
				c.manager.Sync(uid, presence.Patch{DiscordUser: userObj}, false)
			}
		}

	case "GUILD_MEMBER_REMOVE":
		if userObj, ok := d["user"].(map[string]any); ok {
			if uid, ok := userObj["id"].(string); ok {
				c.manager.Remove(uid)
			}
		}

	case "MESSAGE_CREATE":
		if c.cfg.IsIdempotent() && c.onMessage != nil {
			msgCopy := d
			supervisor.Go("bot-message-handler", func() {
				c.onMessage(msgCopy)
			})
		}
	}
}

func (c *Client) processMembersAndPresences(d map[string]any) {
	// Index presences by user.id in O(n)
	presenceMap := make(map[string]map[string]any)
	if presencesList, ok := d["presences"].([]any); ok {
		for _, pItem := range presencesList {
			if p, ok := pItem.(map[string]any); ok {
				if userObj, ok := p["user"].(map[string]any); ok {
					if uid, ok := userObj["id"].(string); ok {
						presenceMap[uid] = p
					}
				}
			}
		}
	}

	if membersList, ok := d["members"].([]any); ok {
		for _, mItem := range membersList {
			if m, ok := mItem.(map[string]any); ok {
				if userObj, ok := m["user"].(map[string]any); ok {
					if uid, ok := userObj["id"].(string); ok {
						pres := presenceMap[uid]
						c.manager.Upsert(uid, userObj, pres)
					}
				}
			}
		}
	}
}
