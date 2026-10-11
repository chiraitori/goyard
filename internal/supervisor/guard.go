package supervisor

import (
	"log/slog"
	"runtime/debug"
	"sync/atomic"
)

var panicsTotal atomic.Uint64

func PanicsTotal() uint64 {
	return panicsTotal.Load()
}

func RecoverAndLog(name string) {
	if r := recover(); r != nil {
		panicsTotal.Add(1)
		slog.Error("goroutine panicked",
			"component", name,
			"error", r,
			"stack", string(debug.Stack()),
		)
	}
}

// Go runs fn in a new goroutine protected by a panic guard.
func Go(name string, fn func()) {
	go func() {
		defer RecoverAndLog(name)
		fn()
	}()
}
