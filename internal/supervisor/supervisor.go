package supervisor

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"
)

// ErrFatal can be returned by a supervised function to trigger long backoff (e.g. 5 min)
var ErrFatal = errors.New("fatal component error")

type SupervisorOptions struct {
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	FatalBackoff   time.Duration
	ResetDuration  time.Duration
}

type Option func(*SupervisorOptions)

func WithFatalBackoff(d time.Duration) Option {
	return func(o *SupervisorOptions) {
		o.FatalBackoff = d
	}
}

// Supervise runs fn until ctx is cancelled. If fn returns or panics it logs and restarts it
// with exponential backoff (initial 1s, factor 2, max 30s, jitter +-20%).
// Backoff resets if fn ran for longer than 60s.
func Supervise(ctx context.Context, name string, fn func(ctx context.Context) error, opts ...Option) {
	options := SupervisorOptions{
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		FatalBackoff:   5 * time.Minute,
		ResetDuration:  60 * time.Second,
	}
	for _, opt := range opts {
		opt(&options)
	}

	backoff := options.InitialBackoff

	for {
		if ctx.Err() != nil {
			return
		}

		startTime := time.Now()
		var err error

		func() {
			defer RecoverAndLog(name)
			err = fn(ctx)
		}()

		if ctx.Err() != nil {
			return
		}

		if time.Since(startTime) > options.ResetDuration {
			backoff = options.InitialBackoff
		}

		isFatal := errors.Is(err, ErrFatal)
		var sleepDuration time.Duration

		if isFatal {
			slog.Error("supervised component encountered fatal error; backing off",
				"component", name,
				"error", err,
				"backoff", options.FatalBackoff,
			)
			sleepDuration = options.FatalBackoff
		} else {
			jitter := (rand.Float64()*0.4 - 0.2) // +-20%
			sleepDuration = time.Duration(float64(backoff) * (1.0 + jitter))
			slog.Warn("supervised component exited; restarting",
				"component", name,
				"error", err,
				"backoff", sleepDuration,
			)

			backoff *= 2
			if backoff > options.MaxBackoff {
				backoff = options.MaxBackoff
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(sleepDuration):
		}
	}
}
