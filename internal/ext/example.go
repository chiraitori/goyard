package ext

import (
	"fmt"
	"net/http"

	"goyard/internal/bot"
)

// ExampleExtension provides a ping route and a bot ping command
type ExampleExtension struct{}

func (e *ExampleExtension) Name() string {
	return "example"
}

func (e *ExampleExtension) Routes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /ext/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"pong":true,"monitored":%d}`, d.Presence.Count())
	})
}

func (e *ExampleExtension) OnPresenceUpdate(userID string, prettyJSON []byte, d Deps) {
	// Example hook: can publish custom WS event
	_ = d.Hub.PublishCustom(userID, "CUSTOM_EXT_EVENT", map[string]string{"user": userID})
}

func (e *ExampleExtension) OnBotCommand(cmd string, args []string, msg bot.Message, d Deps) bool {
	if cmd == "ping" {
		d.Logger.Info("received ext ping bot command", "author", msg.AuthorID)
		return true
	}
	return false
}
