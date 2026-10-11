package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"goyard/internal/config"
	"goyard/internal/hub"
	"goyard/internal/presence"
	"goyard/internal/store"
	"goyard/internal/supervisor"
)

func TestGatewayClientWithFakeDiscord(t *testing.T) {
	cfg := &config.Config{
		BotToken:        "fake_secret_token",
		BotPresence:     "testing",
		BotPresenceType: 3,
	}
	h := hub.NewHub()
	s := store.NewMemoryStore()
	m := presence.NewManager(h, s)

	var lastClientOp atomic.Int32
	var receivedIdentify atomic.Bool
	var receivedResume atomic.Bool
	var receivedGuildMembers atomic.Bool

	// Fake Discord Gateway Server
	fakeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "")

		ctx := r.Context()

		// 1. Send Op 10 Hello
		helloPayload := `{"op":10,"d":{"heartbeat_interval":50}}`
		_ = ws.Write(ctx, websocket.MessageText, []byte(helloPayload))

		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var env struct {
				Op int             `json:"op"`
				D  json.RawMessage `json:"d"`
			}
			_ = json.Unmarshal(data, &env)
			lastClientOp.Store(int32(env.Op))

			switch env.Op {
			case OpIdentify:
				receivedIdentify.Store(true)
				// Send READY
				ready := `{"op":0,"s":1,"t":"READY","d":{"session_id":"sess_123","resume_gateway_url":"` + r.Host + `"}}`
				_ = ws.Write(ctx, websocket.MessageText, []byte(ready))

				// Send GUILD_CREATE
				guild := `{
					"op":0,"s":2,"t":"GUILD_CREATE","d":{
						"id":"g1",
						"members":[
							{"user":{"id":"u100","username":"user100"}}
						],
						"presences":[
							{"user":{"id":"u100"},"status":"online"}
						]
					}
				}`
				_ = ws.Write(ctx, websocket.MessageText, []byte(guild))

			case OpResume:
				receivedResume.Store(true)

			case OpHeartbeat:
				// ACK
				_ = ws.Write(ctx, websocket.MessageText, []byte(`{"op":11}`))

			case OpRequestGuildMembers:
				receivedGuildMembers.Store(true)
			}
		}
	}))
	defer fakeServer.Close()

	gwURL := "ws" + strings.TrimPrefix(fakeServer.URL, "http")

	client := NewClient(cfg, m, nil)
	client.SetGatewayURL(gwURL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	supervisor.Go("test-gw", func() {
		_ = client.Run(ctx)
	})

	// Wait for identify and guild member processing
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if receivedIdentify.Load() && receivedGuildMembers.Load() && m.Count() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !receivedIdentify.Load() {
		t.Fatal("expected gateway client to send Identify")
	}
	if !receivedGuildMembers.Load() {
		t.Fatal("expected gateway client to send RequestGuildMembers")
	}
	if m.Count() != 1 {
		t.Fatalf("expected manager to have 1 tracked member, got %d", m.Count())
	}

	pretty, ok := m.Get("u100")
	if !ok || !strings.Contains(string(pretty), `"discord_status":"online"`) {
		t.Fatalf("expected online status in tracked user: %s", string(pretty))
	}
}

func TestGateway10000MembersPerformance(t *testing.T) {
	h := hub.NewHub()
	s := store.NewMemoryStore()
	m := presence.NewManager(h, s)
	client := NewClient(&config.Config{}, m, nil)

	// Build fake GUILD_CREATE with 10 000 members
	start := time.Now()
	members := make([]any, 10000)
	presences := make([]any, 10000)

	for i := 0; i < 10000; i++ {
		uid := fmt.Sprintf("bench_user_%d", i)
		members[i] = map[string]any{
			"user": map[string]any{
				"id":       uid,
				"username": fmt.Sprintf("bench_%d", i),
			},
		}
		presences[i] = map[string]any{
			"user":   map[string]any{"id": uid},
			"status": "online",
		}
	}

	guildData := map[string]any{
		"id":        "bench_guild",
		"members":   members,
		"presences": presences,
	}

	client.processMembersAndPresences(guildData)

	elapsed := time.Since(start)
	t.Logf("10,000 members processed in %v", elapsed)

	if elapsed > 1*time.Second {
		t.Errorf("expected 10,000 members to process in under 1 second, took %v", elapsed)
	}
	if m.Count() != 10000 {
		t.Fatalf("expected 10000 monitored users, got %d", m.Count())
	}
}
