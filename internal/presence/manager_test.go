package presence

import (
	"context"
	"testing"
	"time"

	"goyard/internal/hub"
	"goyard/internal/store"
)

func TestManagerLifecycleAndSync(t *testing.T) {
	h := hub.NewHub()
	s := store.NewMemoryStore()
	m := NewManager(h, s)

	// Pre-populate store KV
	_ = s.SetKV(context.Background(), "user_1", "theme", "dark")

	// Global subscriber to observe creation
	globalClient := hub.NewClient(h.NextID(), false, nil)
	h.AddClient(globalClient)
	h.SubscribeAll(globalClient)

	// Upsert new user
	m.Upsert("user_1", map[string]any{"id": "user_1", "username": "alice"}, nil)

	if m.Count() != 1 {
		t.Fatalf("expected count 1, got %d", m.Count())
	}

	// Global client receives creation event
	select {
	case f := <-globalClient.Send:
		if len(f.Data) == 0 {
			t.Fatal("empty event received")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for global creation event")
	}

	// Get user presence
	pretty, ok := m.Get("user_1")
	if !ok || len(pretty) == 0 {
		t.Fatalf("failed to get user_1 presence: ok=%v", ok)
	}

	// Sync presence update
	m.Sync("user_1", Patch{
		DiscordPresence: map[string]any{"status": "idle"},
	}, false)

	prettyAfter, _ := m.Get("user_1")
	if string(prettyAfter) == string(pretty) {
		t.Fatal("expected prettyJSON to change after sync")
	}

	// Snapshot all
	all := m.SnapshotAll()
	if len(all) != 1 || all["user_1"] == nil {
		t.Fatalf("expected snapshot to contain user_1")
	}

	// Remove
	m.Remove("user_1")
	if m.Count() != 0 {
		t.Fatalf("expected count 0 after removal, got %d", m.Count())
	}
	if _, ok := m.Get("user_1"); ok {
		t.Fatal("user_1 should not exist after removal")
	}
}
