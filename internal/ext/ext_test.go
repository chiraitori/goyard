package ext

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"goyard/internal/bot"
	"goyard/internal/hub"
	"goyard/internal/presence"
	"goyard/internal/store"
)

func TestExtension(t *testing.T) {
	h := hub.NewHub()
	s := store.NewMemoryStore()
	m := presence.NewManager(h, s)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	deps := Deps{
		Presence: m,
		Store:    s,
		Hub:      h,
		Logger:   logger,
	}

	example := &ExampleExtension{}
	reg := NewRegistry(deps, example)

	// 1. Test custom route
	mux := http.NewServeMux()
	reg.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/ext/ping", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != `{"pong":true,"monitored":0}` {
		t.Errorf("unexpected body: %s", w.Body.String())
	}

	// 2. Test bot command intercept
	handled := reg.DispatchBotCommand("ping", []string{}, bot.Message{AuthorID: "u1"})
	if !handled {
		t.Error("expected ping command to be handled by extension")
	}

	notHandled := reg.DispatchBotCommand("unknown", []string{}, bot.Message{})
	if notHandled {
		t.Error("expected unknown command not to be handled")
	}

	// 3. Test presence update hook
	reg.DispatchPresenceUpdate("u1", []byte(`{}`))
	time.Sleep(50 * time.Millisecond)
}
