package hub

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"goyard/internal/metrics"
)

type Frame struct {
	IsZlib bool
	Data   []byte
}

type Client struct {
	ID      uint64
	Send    chan Frame
	Zlib    bool
	Subs    map[string]struct{}
	All     bool
	CloseFn func(code int, reason string)

	mu sync.Mutex
}

func NewClient(id uint64, zlib bool, closeFn func(code int, reason string)) *Client {
	return &Client{
		ID:      id,
		Send:    make(chan Frame, 64),
		Zlib:    zlib,
		Subs:    make(map[string]struct{}),
		CloseFn: closeFn,
	}
}

type Hub struct {
	mu     sync.RWMutex
	byUser map[string]map[*Client]struct{}
	global map[*Client]struct{}

	nextClientID atomic.Uint64
}

func NewHub() *Hub {
	return &Hub{
		byUser: make(map[string]map[*Client]struct{}),
		global: make(map[*Client]struct{}),
	}
}

func (h *Hub) NextID() uint64 {
	return h.nextClientID.Add(1)
}

func (h *Hub) AddClient(c *Client) {
	metrics.DefaultMetrics.ConnectedSessions.Add(1)
}

func (h *Hub) RemoveClient(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.global, c)
	for u := range c.Subs {
		if set, ok := h.byUser[u]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(h.byUser, u)
			}
		}
	}
	clear(c.Subs)
	metrics.DefaultMetrics.ConnectedSessions.Add(-1)
}

func (h *Hub) SubscribeUser(c *Client, userID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	c.mu.Lock()
	c.Subs[userID] = struct{}{}
	c.mu.Unlock()

	set, ok := h.byUser[userID]
	if !ok {
		set = make(map[*Client]struct{})
		h.byUser[userID] = set
	}
	set[c] = struct{}{}
}

func (h *Hub) SubscribeAll(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	c.mu.Lock()
	c.All = true
	c.mu.Unlock()

	h.global[c] = struct{}{}
}

func (h *Hub) UnsubscribeUser(c *Client, userID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	c.mu.Lock()
	delete(c.Subs, userID)
	c.mu.Unlock()

	if set, ok := h.byUser[userID]; ok {
		delete(set, c)
		if len(set) == 0 {
			delete(h.byUser, userID)
		}
	}
}

func (h *Hub) UnsubscribeAll(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	c.mu.Lock()
	c.All = false
	for u := range c.Subs {
		if set, ok := h.byUser[u]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(h.byUser, u)
			}
		}
	}
	clear(c.Subs)
	c.mu.Unlock()

	delete(h.global, c)
}

// CompressZlib compresses data using RFC 1950 zlib
func CompressZlib(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Publish sends PRESENCE_UPDATE to subscribers of userID and to global subscribers.
// Each client receives the message at most once.
func (h *Hub) Publish(userID string, prettyJSON []byte) {
	msg := fmt.Sprintf(`{"op":0,"t":"PRESENCE_UPDATE","d":%s}`, string(prettyJSON))
	h.broadcast(userID, []byte(msg), false)
}

// PublishGlobalOnly sends PRESENCE_UPDATE to global subscribers only (used for new user registration).
func (h *Hub) PublishGlobalOnly(userID string, prettyJSON []byte) {
	msg := fmt.Sprintf(`{"op":0,"t":"PRESENCE_UPDATE","d":%s}`, string(prettyJSON))
	h.broadcast(userID, []byte(msg), true)
}

// PublishCustom allows extensions to send custom events.
func (h *Hub) PublishCustom(userID, eventName string, payload any) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	msg := fmt.Sprintf(`{"op":0,"t":"%s","d":%s}`, eventName, string(payloadBytes))
	h.broadcast(userID, []byte(msg), false)
	return nil
}

func (h *Hub) broadcast(userID string, textData []byte, globalOnly bool) {
	metrics.DefaultMetrics.PresenceUpdatesTotal.Add(1)

	// Collect unique clients
	h.mu.RLock()
	clientSet := make(map[*Client]struct{})

	for c := range h.global {
		clientSet[c] = struct{}{}
	}

	if !globalOnly && userID != "" {
		if uSet, ok := h.byUser[userID]; ok {
			for c := range uSet {
				clientSet[c] = struct{}{}
			}
		}
	}
	h.mu.RUnlock()

	if len(clientSet) == 0 {
		return
	}

	var zlibData []byte
	var zlibOnce sync.Once
	var zlibErr error

	for c := range clientSet {
		var f Frame
		if c.Zlib {
			zlibOnce.Do(func() {
				zlibData, zlibErr = CompressZlib(textData)
			})
			if zlibErr != nil {
				continue
			}
			f = Frame{IsZlib: true, Data: zlibData}
		} else {
			f = Frame{IsZlib: false, Data: textData}
		}

		select {
		case c.Send <- f:
			metrics.DefaultMetrics.MessagesOutboundTotal.Add(1)
		default:
			// Client send queue full -> slow consumer -> drop
			metrics.DefaultMetrics.SlowConsumersDroppedTotal.Add(1)
			if c.CloseFn != nil {
				c.CloseFn(1013, "slow_consumer")
			}
		}
	}
}
