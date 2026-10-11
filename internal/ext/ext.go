package ext

import (
	"log/slog"
	"net/http"

	"goyard/internal/bot"
	"goyard/internal/hub"
	"goyard/internal/presence"
	"goyard/internal/store"
	"goyard/internal/supervisor"
)

// Deps gives extensions access to the running app.
type Deps struct {
	Presence *presence.Manager
	Store    store.Store
	Hub      *hub.Hub
	Logger   *slog.Logger
}

type Extension interface {
	Name() string
	// Routes may register extra HTTP routes (they get the same CORS/recover middleware).
	Routes(mux *http.ServeMux, d Deps)
	// OnPresenceUpdate is called (in its own guarded goroutine, never blocking the core) after every sync.
	OnPresenceUpdate(userID string, prettyJSON []byte, d Deps)
	// OnBotCommand may handle extra commands; return true if handled.
	OnBotCommand(cmd string, args []string, msg bot.Message, d Deps) bool
}

// Registry manages registered extensions
type Registry struct {
	extensions []Extension
	deps       Deps
}

func NewRegistry(deps Deps, extensions ...Extension) *Registry {
	return &Registry{
		extensions: extensions,
		deps:       deps,
	}
}

func (r *Registry) RegisterRoutes(mux *http.ServeMux) {
	for _, ext := range r.extensions {
		ext.Routes(mux, r.deps)
	}
}

func (r *Registry) DispatchPresenceUpdate(userID string, prettyJSON []byte) {
	for _, ext := range r.extensions {
		e := ext
		supervisor.Go("ext-presence-"+e.Name(), func() {
			e.OnPresenceUpdate(userID, prettyJSON, r.deps)
		})
	}
}

func (r *Registry) DispatchBotCommand(cmd string, args []string, msg bot.Message) bool {
	for _, ext := range r.extensions {
		if ext.OnBotCommand(cmd, args, msg, r.deps) {
			return true
		}
	}
	return false
}
