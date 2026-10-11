package wsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"goyard/internal/config"
	"goyard/internal/hub"
	"goyard/internal/metrics"
	"goyard/internal/presence"
	"goyard/internal/supervisor"
)

type Server struct {
	cfg     *config.Config
	manager *presence.Manager
	hub     *hub.Hub
}

func NewServer(cfg *config.Config, manager *presence.Manager, hub *hub.Hub) *Server {
	return &Server{
		cfg:     cfg,
		manager: manager,
		hub:     hub,
	}
}

func (s *Server) HandleSocket(w http.ResponseWriter, r *http.Request) {
	// 1. Check MAX_SOCKETS limit
	if s.cfg.MaxSockets > 0 && int(metrics.DefaultMetrics.ConnectedSessions.Load()) >= s.cfg.MaxSockets {
		http.Error(w, "Service Unavailable: max sockets reached", http.StatusServiceUnavailable)
		return
	}

	// 2. Compression query parameter
	compression := r.URL.Query().Get("compression") == "zlib_json"

	// 3. Upgrade to WebSocket
	opts := &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	}
	wsConn, err := websocket.Accept(w, r, opts)
	if err != nil {
		metrics.DefaultMetrics.SocketClosesTotal.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"error": map[string]string{
				"code":    "upgrade_failed",
				"message": "Request failed to upgrade",
			},
		})
		return
	}
	defer wsConn.Close(websocket.StatusInternalError, "server_closing")

	wsConn.SetReadLimit(64 * 1024)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var closeOnce sync.Once
	closeFn := func(code int, reason string) {
		closeOnce.Do(func() {
			metrics.DefaultMetrics.SocketClosesTotal.Add(1)
			_ = wsConn.Close(websocket.StatusCode(code), reason)
			cancel()
		})
	}

	client := hub.NewClient(s.hub.NextID(), compression, closeFn)
	s.hub.AddClient(client)
	defer s.hub.RemoveClient(client)

	// Single writer goroutine
	writerDone := make(chan struct{})
	supervisor.Go("ws-writer", func() {
		defer close(writerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case f, ok := <-client.Send:
				if !ok {
					return
				}
				writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
				msgType := websocket.MessageText
				if f.IsZlib {
					msgType = websocket.MessageBinary
				}
				err := wsConn.Write(writeCtx, msgType, f.Data)
				writeCancel()
				if err != nil {
					closeFn(int(websocket.StatusGoingAway), "write_error")
					return
				}
			}
		}
	})

	// Send op 1 Hello immediately: {"op":1,"d":{"heartbeat_interval":30000}}
	helloMsg := []byte(`{"op":1,"d":{"heartbeat_interval":30000}}`)
	if client.Zlib {
		zData, err := hub.CompressZlib(helloMsg)
		if err == nil {
			client.Send <- hub.Frame{IsZlib: true, Data: zData}
		}
	} else {
		client.Send <- hub.Frame{IsZlib: false, Data: helloMsg}
	}
	metrics.DefaultMetrics.MessagesOutboundTotal.Add(1)

	// Reader loop with idle timeout (60s)
	// We reset a 60-second timer on every received message
	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()

	supervisor.Go("ws-idle-timer", func() {
		select {
		case <-timer.C:
			closeFn(int(websocket.StatusPolicyViolation), "heartbeat_timeout")
		case <-ctx.Done():
		}
	})

	for {
		msgType, data, err := wsConn.Read(ctx)
		if err != nil {
			closeFn(int(websocket.StatusNormalClosure), "client_closed")
			break
		}

		// Reset idle timer
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(60 * time.Second)

		metrics.DefaultMetrics.MessagesInboundTotal.Add(1)

		if msgType != websocket.MessageText {
			continue
		}

		var payload struct {
			Op int             `json:"op"`
			D  json.RawMessage `json:"d"`
		}

		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.UseNumber()
		if err := dec.Decode(&payload); err != nil {
			closeFn(4006, "invalid_payload")
			break
		}

		if shouldBreak := s.handleInbound(ctx, client, payload.Op, payload.D, closeFn); shouldBreak {
			break
		}
	}

	<-writerDone
}

func (s *Server) handleInbound(ctx context.Context, client *hub.Client, op int, d json.RawMessage, closeFn func(code int, reason string)) bool {
	switch op {
	case 2: // Initialize
		if len(d) == 0 {
			closeFn(4005, "requires_data_object")
			return true
		}
		var rawObj map[string]any
		dec := json.NewDecoder(strings.NewReader(string(d)))
		dec.UseNumber()
		if err := dec.Decode(&rawObj); err != nil || len(rawObj) == 0 {
			closeFn(4005, "requires_data_object")
			return true
		}

		// 1. subscribe_to_ids (array of strings)
		if idsVal, ok := rawObj["subscribe_to_ids"]; ok {
			arr, isArr := idsVal.([]any)
			if !isArr {
				closeFn(4006, "invalid_payload")
				return true
			}
			initState := make(map[string]json.RawMessage)
			for _, item := range arr {
				id, ok := item.(string)
				if !ok {
					continue
				}
				s.hub.SubscribeUser(client, id)
				if pretty, exists := s.manager.Get(id); exists {
					initState[id] = json.RawMessage(pretty)
				}
			}
			s.sendInitState(client, initState)
			return false
		}

		// 2. subscribe_to_id (string)
		if idVal, ok := rawObj["subscribe_to_id"]; ok {
			id, isStr := idVal.(string)
			if !isStr {
				closeFn(4006, "invalid_payload")
				return true
			}
			if pretty, exists := s.manager.Get(id); exists {
				s.hub.SubscribeUser(client, id)
				s.sendRawInitState(client, pretty)
			} else {
				s.sendRawInitState(client, []byte(`{}`))
			}
			return false
		}

		// 3. subscribe_to_all == true
		if allVal, ok := rawObj["subscribe_to_all"]; ok {
			isTrue, okBool := allVal.(bool)
			if okBool && isTrue {
				s.hub.SubscribeAll(client)
				initState := s.manager.SnapshotAll()
				s.sendInitState(client, initState)
				return false
			}
		}

		// None of the above matched
		closeFn(4006, "invalid_payload")
		return true

	case 3: // Heartbeat
		// Resets idle timer (handled in caller loop)
		return false

	case 4: // Unsubscribe
		if len(d) > 0 {
			var unsubs struct {
				UnsubscribeFromIDs []string `json:"unsubscribe_from_ids"`
				UnsubscribeFromID  string   `json:"unsubscribe_from_id"`
				UnsubscribeFromAll bool     `json:"unsubscribe_from_all"`
			}
			_ = json.Unmarshal(d, &unsubs)

			if unsubs.UnsubscribeFromAll {
				s.hub.UnsubscribeAll(client)
			} else {
				if unsubs.UnsubscribeFromID != "" {
					s.hub.UnsubscribeUser(client, unsubs.UnsubscribeFromID)
				}
				for _, id := range unsubs.UnsubscribeFromIDs {
					s.hub.UnsubscribeUser(client, id)
				}
			}
		}
		return false

	default: // Unknown opcode
		slog.Debug("unknown ws opcode", "op", op)
		closeFn(4004, "unknown_opcode")
		return true
	}
}

func (s *Server) sendInitState(client *hub.Client, initState map[string]json.RawMessage) {
	dataBytes, _ := json.Marshal(initState)
	s.sendRawInitState(client, dataBytes)
}

func (s *Server) sendRawInitState(client *hub.Client, rawD []byte) {
	msg := fmt.Sprintf(`{"op":0,"t":"INIT_STATE","d":%s}`, string(rawD))
	textData := []byte(msg)
	if client.Zlib {
		zData, err := hub.CompressZlib(textData)
		if err == nil {
			client.Send <- hub.Frame{IsZlib: true, Data: zData}
		}
	} else {
		client.Send <- hub.Frame{IsZlib: false, Data: textData}
	}
	metrics.DefaultMetrics.MessagesOutboundTotal.Add(1)
}
