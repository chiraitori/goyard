package bot

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"goyard/internal/config"
	"goyard/internal/discord"
	"goyard/internal/hub"
	"goyard/internal/presence"
	"goyard/internal/store"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func setupTestBot(t *testing.T) (*Handler, *presence.Manager, store.Store, *[]string) {
	cfg := &config.Config{
		CommandPrefix: ".",
		ExternalURL:   "https://api.lanyard.rest",
		BotToken:      "fake_token",
	}
	h := hub.NewHub()
	s := store.NewMemoryStore()
	m := presence.NewManager(h, s)

	var sentMessages []string
	var mu sync.Mutex

	fakeDiscord := discord.NewRESTClient(cfg.BotToken)
	fakeDiscord.SetHTTPClient(&http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			sentMessages = append(sentMessages, string(body))
			mu.Unlock()

			if strings.Contains(r.URL.Path, "/channels") && r.Method == http.MethodPost {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"id":"dm_123"}`)),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})

	botHandler := NewHandler(cfg, m, s, fakeDiscord)
	return botHandler, m, s, &sentMessages
}

func TestBotCommands(t *testing.T) {
	botHandler, m, s, sent := setupTestBot(t)

	// Pre-monitor user
	m.Upsert("u123", map[string]any{"id": "u123", "username": "alice"}, map[string]any{"status": "online"})

	t.Run("Ignore bot messages", func(t *testing.T) {
		botHandler.HandleMessage(map[string]any{
			"content":    ".help",
			"channel_id": "c1",
			"author": map[string]any{
				"id":  "u123",
				"bot": true,
			},
		})
		time.Sleep(50 * time.Millisecond)
		if len(*sent) != 0 {
			t.Errorf("expected 0 messages for bot user")
		}
	})

	t.Run("Set and Get KV via bot", func(t *testing.T) {
		// Set
		botHandler.HandleMessage(map[string]any{
			"content":    ".set location Paris, France",
			"channel_id": "c1",
			"author": map[string]any{
				"id":  "u123",
				"bot": false,
			},
		})
		time.Sleep(50 * time.Millisecond)

		kv, _ := s.GetKV(context.Background(), "u123")
		if kv["location"] != "Paris, France" {
			t.Fatalf("expected Paris, France, got %s", kv["location"])
		}

		// Get
		botHandler.HandleMessage(map[string]any{
			"content":    ".get location",
			"channel_id": "c1",
			"author": map[string]any{
				"id":  "u123",
				"bot": false,
			},
		})
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("API key leak detection", func(t *testing.T) {
		apiKey, _ := s.RotateKey(context.Background(), "u123")

		// User accidentally types key in set command
		botHandler.HandleMessage(map[string]any{
			"content":    ".set mykey " + apiKey,
			"channel_id": "c1",
			"author": map[string]any{
				"id":  "u123",
				"bot": false,
			},
		})
		time.Sleep(100 * time.Millisecond)

		// Key must be rotated
		currentKey, _, _ := s.KeyForUser(context.Background(), "u123")
		if currentKey == apiKey {
			t.Fatalf("key was not rotated after leak!")
		}
	})

	t.Run("ApiKey command rejected in guilds", func(t *testing.T) {
		botHandler.HandleMessage(map[string]any{
			"content":    ".apikey",
			"channel_id": "c1",
			"guild_id":   "g1",
			"author": map[string]any{
				"id":  "u123",
				"bot": false,
			},
		})
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("Sanitize @ mentions", func(t *testing.T) {
		res := SanitizeContent("Hello @everyone and @here")
		if strings.Contains(res, "@everyone") || strings.Contains(res, "@here") {
			t.Errorf("mentions not sanitized: %s", res)
		}
		if !strings.Contains(res, "@\u200beveryone") {
			t.Errorf("zero-width space not injected: %s", res)
		}
	})
}
