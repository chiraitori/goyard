package supervisor

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestPanicGuard(t *testing.T) {
	done := make(chan struct{})
	before := PanicsTotal()

	Go("test-panic", func() {
		defer close(done)
		panic("testing panic guard")
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("goroutine did not complete")
	}

	// Give the outer defer RecoverAndLog time to execute
	for i := 0; i < 50; i++ {
		if PanicsTotal() > before {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if PanicsTotal() != before+1 {
		t.Errorf("expected PanicsTotal to increase, got %d", PanicsTotal())
	}
}

func TestSupervise(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var runs atomic.Int32
	opts := []Option{
		func(o *SupervisorOptions) {
			o.InitialBackoff = 10 * time.Millisecond
			o.MaxBackoff = 20 * time.Millisecond
		},
	}

	Go("test-supervisor", func() {
		Supervise(ctx, "worker", func(c context.Context) error {
			if runs.Add(1) >= 3 {
				cancel()
				return nil
			}
			return errors.New("simulated error")
		}, opts...)
	})

	select {
	case <-ctx.Done():
		if runs.Load() < 3 {
			t.Errorf("expected at least 3 runs, got %d", runs.Load())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not stop as expected")
	}
}
