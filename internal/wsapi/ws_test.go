package wsapi

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"goyard/internal/config"
	"goyard/internal/hub"
	"goyard/internal/presence"
	"goyard/internal/store"
)

func decompress(t *testing.T, data []byte) []byte {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("failed to decompress: %v", err)
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	return out
}

func setupTestWSServer(t *testing.T) (*Server, *presence.Manager, *httptest.Server) {
	cfg := &config.Config{
		MaxSockets: 100,
	}
	h := hub.NewHub()
	s := store.NewMemoryStore()
	m := presence.NewManager(h, s)
	wsServer := NewServer(cfg, m, h)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /socket", wsServer.HandleSocket)
	ts := httptest.NewServer(mux)

	return wsServer, m, ts
}

func TestWebSocketProtocol(t *testing.T) {
	_, m, ts := setupTestWSServer(t)
	defer ts.Close()

	// Preload a user
	m.Upsert("u1", map[string]any{"id": "u1", "username": "alice"}, map[string]any{"status": "online"})

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/socket"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("Connect and receive Hello (op 1)", func(t *testing.T) {
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read failed: %v", err)
		}
		var hello struct {
			Op int `json:"op"`
			D  struct {
				HeartbeatInterval int `json:"heartbeat_interval"`
			} `json:"d"`
		}
		json.Unmarshal(data, &hello)
		if hello.Op != 1 || hello.D.HeartbeatInterval != 30000 {
			t.Errorf("unexpected hello payload: %s", string(data))
		}
	})

	t.Run("Initialize single user (subscribe_to_id)", func(t *testing.T) {
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		// Read hello
		_, _, _ = conn.Read(ctx)

		// Send op 2 with subscribe_to_id
		initPayload := `{"op":2,"d":{"subscribe_to_id":"u1"}}`
		err = conn.Write(ctx, websocket.MessageText, []byte(initPayload))
		if err != nil {
			t.Fatalf("write failed: %v", err)
		}

		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read init state failed: %v", err)
		}
		var event struct {
			Op int             `json:"op"`
			T  string          `json:"t"`
			D  json.RawMessage `json:"d"`
		}
		json.Unmarshal(data, &event)
		if event.Op != 0 || event.T != "INIT_STATE" {
			t.Fatalf("expected INIT_STATE, got %s", string(data))
		}

		// Now trigger presence update on manager and verify live event
		m.Sync("u1", presence.Patch{
			DiscordPresence: map[string]any{"status": "idle"},
		}, false)

		_, updateData, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("failed to read presence update: %v", err)
		}
		json.Unmarshal(updateData, &event)
		if event.Op != 0 || event.T != "PRESENCE_UPDATE" {
			t.Fatalf("expected PRESENCE_UPDATE, got %s", string(updateData))
		}
	})

	t.Run("Initialize multiple users (subscribe_to_ids)", func(t *testing.T) {
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		_, _, _ = conn.Read(ctx)

		initPayload := `{"op":2,"d":{"subscribe_to_ids":["u1", "u_unknown"]}}`
		err = conn.Write(ctx, websocket.MessageText, []byte(initPayload))
		if err != nil {
			t.Fatalf("write failed: %v", err)
		}

		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read failed: %v", err)
		}
		var event struct {
			Op int                        `json:"op"`
			T  string                     `json:"t"`
			D  map[string]json.RawMessage `json:"d"`
		}
		json.Unmarshal(data, &event)
		if event.Op != 0 || event.T != "INIT_STATE" {
			t.Fatalf("expected INIT_STATE, got %s", string(data))
		}
		if _, ok := event.D["u1"]; !ok {
			t.Errorf("expected u1 in map")
		}
		if _, ok := event.D["u_unknown"]; ok {
			t.Errorf("u_unknown should not be in map")
		}
	})

	t.Run("Initialize all (subscribe_to_all)", func(t *testing.T) {
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		_, _, _ = conn.Read(ctx)

		initPayload := `{"op":2,"d":{"subscribe_to_all":true}}`
		err = conn.Write(ctx, websocket.MessageText, []byte(initPayload))
		if err != nil {
			t.Fatalf("write failed: %v", err)
		}

		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read failed: %v", err)
		}
		var event struct {
			Op int                        `json:"op"`
			T  string                     `json:"t"`
			D  map[string]json.RawMessage `json:"d"`
		}
		json.Unmarshal(data, &event)
		if event.Op != 0 || event.T != "INIT_STATE" {
			t.Fatalf("expected INIT_STATE, got %s", string(data))
		}
		if len(event.D) == 0 {
			t.Errorf("expected non-empty snapshot")
		}
	})

	t.Run("Zlib compression mode", func(t *testing.T) {
		zlibURL := wsURL + "?compression=zlib_json"
		conn, _, err := websocket.Dial(ctx, zlibURL, nil)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		msgType, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read failed: %v", err)
		}
		if msgType != websocket.MessageBinary {
			t.Errorf("expected binary message for zlib, got %v", msgType)
		}
		decompressed := decompress(t, data)
		if !strings.Contains(string(decompressed), `"heartbeat_interval":30000`) {
			t.Errorf("unexpected decompressed data: %s", string(decompressed))
		}
	})

	t.Run("Close codes for errors", func(t *testing.T) {
		// 1. Unknown opcode -> 4004
		conn1, _, _ := websocket.Dial(ctx, wsURL, nil)
		_, _, _ = conn1.Read(ctx)
		_ = conn1.Write(ctx, websocket.MessageText, []byte(`{"op":99}`))
		_, _, err := conn1.Read(ctx)
		if websocket.CloseStatus(err) != 4004 {
			t.Errorf("expected close code 4004 for unknown opcode, got %v", err)
		}

		// 2. Op 2 empty d -> 4005
		conn2, _, _ := websocket.Dial(ctx, wsURL, nil)
		_, _, _ = conn2.Read(ctx)
		_ = conn2.Write(ctx, websocket.MessageText, []byte(`{"op":2,"d":{}}`))
		_, _, err = conn2.Read(ctx)
		if websocket.CloseStatus(err) != 4005 {
			t.Errorf("expected close code 4005 for empty d, got %v", err)
		}

		// 3. Op 2 invalid payload -> 4006
		conn3, _, _ := websocket.Dial(ctx, wsURL, nil)
		_, _, _ = conn3.Read(ctx)
		_ = conn3.Write(ctx, websocket.MessageText, []byte(`{"op":2,"d":{"bad_field":123}}`))
		_, _, err = conn3.Read(ctx)
		if websocket.CloseStatus(err) != 4006 {
			t.Errorf("expected close code 4006 for bad payload, got %v", err)
		}
	})
}
