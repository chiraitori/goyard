package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"goyard/internal/config"
	"goyard/internal/hub"
	"goyard/internal/presence"
	"goyard/internal/store"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func setupTestAPI(t *testing.T) (*API, *presence.Manager, store.Store, http.Handler) {
	cfg := &config.Config{
		DiscordInvite: "https://discord.gg/lanyard",
	}
	h := hub.NewHub()
	s := store.NewMemoryStore()
	m := presence.NewManager(h, s)
	api := NewAPI(cfg, m, s)

	mux := http.NewServeMux()
	api.RegisterRoutes(mux)
	handler := api.Middleware(mux)

	return api, m, s, handler
}

func TestAPIRoutes(t *testing.T) {
	api, m, s, handler := setupTestAPI(t)

	// Preload a user
	user1 := map[string]any{
		"id":            "1001",
		"username":      "alice",
		"discriminator": "0",
		"avatar":        "a_abcdef123456",
	}
	m.Upsert("1001", user1, map[string]any{"status": "online"})
	apiKey, _ := s.RotateKey(context.Background(), "1001")

	t.Run("CORS and OPTIONS", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/v1/users/1001", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected 204 for OPTIONS, got %d", w.Code)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("expected CORS origin *")
		}
	})

	t.Run("GET / root info", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		data := resp["data"].(map[string]any)
		if data["discord_invite"] != "https://discord.gg/lanyard" {
			t.Errorf("unexpected invite: %v", data["discord_invite"])
		}
	})

	t.Run("GET /v1/users/1001 tracked user", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/users/1001", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["success"] != true {
			t.Errorf("expected success true")
		}
	})

	t.Run("GET /v1/users/9999 untracked user", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/users/9999", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), "user_not_monitored") {
			t.Errorf("expected user_not_monitored code: %s", w.Body.String())
		}
	})

	t.Run("GET /v1/users/@me with and without auth", func(t *testing.T) {
		// Without auth
		req := httptest.NewRequest("GET", "/v1/users/@me", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 without auth, got %d", w.Code)
		}

		// With valid auth
		req = httptest.NewRequest("GET", "/v1/users/@me", nil)
		req.Header.Set("Authorization", apiKey)
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200 with auth, got %d", w.Code)
		}
	})

	t.Run("PUT and DELETE KV", func(t *testing.T) {
		// PUT valid key
		req := httptest.NewRequest("PUT", "/v1/users/@me/kv/location", strings.NewReader("Earth"))
		req.Header.Set("Authorization", apiKey)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusNoContent {
			t.Fatalf("expected 204 for PUT, got %d, body: %s", w.Code, w.Body.String())
		}

		// Verify KV was updated in manager
		prettyBytes, _ := m.Get("1001")
		if !strings.Contains(string(prettyBytes), `"location":"Earth"`) {
			t.Errorf("expected location:Earth in presence: %s", string(prettyBytes))
		}

		// DELETE key
		req = httptest.NewRequest("DELETE", "/v1/users/@me/kv/location", nil)
		req.Header.Set("Authorization", apiKey)
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusNoContent {
			t.Fatalf("expected 204 for DELETE, got %d", w.Code)
		}
	})

	t.Run("PATCH KV valid and invalid", func(t *testing.T) {
		// Non-string value in PATCH -> 404 invalid_kv_value
		badBody := `{"number_key": 123}`
		req := httptest.NewRequest("PATCH", "/v1/users/1001/kv", strings.NewReader(badBody))
		req.Header.Set("Authorization", apiKey)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for bad PATCH, got %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), "invalid_kv_value") {
			t.Errorf("expected invalid_kv_value: %s", w.Body.String())
		}

		// Valid PATCH
		goodBody := `{"k1": "v1", "k2": "v2"}`
		req = httptest.NewRequest("PATCH", "/v1/users/1001/kv", strings.NewReader(goodBody))
		req.Header.Set("Authorization", apiKey)
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusNoContent {
			t.Fatalf("expected 204 for valid PATCH, got %d", w.Code)
		}
	})

	t.Run("Quicklink avatar proxy", func(t *testing.T) {
		var requestedURL string
		fakeClient := &http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				requestedURL = r.URL.String()
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("fake_png_data")),
					Header:     http.Header{"Content-Type": []string{"image/png"}},
				}, nil
			}),
		}
		api.SetHTTPClient(fakeClient)

		req := httptest.NewRequest("GET", "/1001.png", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for quicklink, got %d", w.Code)
		}
		if !strings.Contains(requestedURL, "1001/a_abcdef123456.png") {
			t.Errorf("unexpected requested URL: %s", requestedURL)
		}
	})

	t.Run("Panic in handler returns 500", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /panic", func(w http.ResponseWriter, r *http.Request) {
			panic("intentional test panic")
		})
		h := api.Middleware(mux)

		req := httptest.NewRequest("GET", "/panic", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 on panic, got %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), "Internal Server Error") {
			t.Errorf("expected 'Internal Server Error', got %s", w.Body.String())
		}
	})
}
