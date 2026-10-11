package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func runStoreTests(t *testing.T, newStore func(t *testing.T) Store) {
	ctx := context.Background()

	t.Run("SetKV and GetKV basic", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()

		err := s.SetKV(ctx, "user1", "status", "online")
		if err != nil {
			t.Fatalf("unexpected SetKV error: %v", err)
		}

		kv, err := s.GetKV(ctx, "user1")
		if err != nil {
			t.Fatalf("unexpected GetKV error: %v", err)
		}
		if kv["status"] != "online" {
			t.Errorf("expected 'online', got %s", kv["status"])
		}

		// DelKV
		err = s.DelKV(ctx, "user1", "status")
		if err != nil {
			t.Fatalf("unexpected DelKV error: %v", err)
		}
		kv, _ = s.GetKV(ctx, "user1")
		if _, ok := kv["status"]; ok {
			t.Error("expected status key to be deleted")
		}

		// Delete non-existent key should not error
		err = s.DelKV(ctx, "user1", "not_exist")
		if err != nil {
			t.Errorf("expected nil error on deleting missing key, got %v", err)
		}
	})

	t.Run("Validation errors", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()

		// Key empty
		err := s.SetKV(ctx, "u1", "", "val")
		if err == nil || err.Error() != ErrKeyInvalid.Error() {
			t.Errorf("expected ErrKeyInvalid for empty key, got %v", err)
		}

		// Key invalid characters
		err = s.SetKV(ctx, "u1", "key-with-dashes!", "val")
		if err == nil || err.Error() != ErrKeyInvalid.Error() {
			t.Errorf("expected ErrKeyInvalid, got %v", err)
		}

		// Key too long (> 255)
		longKey := strings.Repeat("a", 256)
		err = s.SetKV(ctx, "u1", longKey, "val")
		if err == nil || err.Error() != ErrKeyTooLong.Error() {
			t.Errorf("expected ErrKeyTooLong, got %v", err)
		}

		// Value too long (> 30000 runes)
		longVal := strings.Repeat("x", 30001)
		err = s.SetKV(ctx, "u1", "valid_key", longVal)
		if err == nil || err.Error() != ErrValueTooLong.Error() {
			t.Errorf("expected ErrValueTooLong, got %v", err)
		}
	})

	t.Run("Key count limits and single/multi error messages", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()

		// Fill up 512 keys
		batch := make(map[string]string)
		for i := 0; i < 512; i++ {
			batch[fmt.Sprintf("k_%d", i)] = "v"
		}
		err := s.MultiSetKV(ctx, "limit_user", batch)
		if err != nil {
			t.Fatalf("failed to fill 512 keys: %v", err)
		}

		// Updating existing key should succeed
		err = s.SetKV(ctx, "limit_user", "k_0", "updated")
		if err != nil {
			t.Fatalf("updating existing key should succeed, got: %v", err)
		}

		// Adding 513th key single
		err = s.SetKV(ctx, "limit_user", "overflow", "val")
		if err == nil || err.Error() != ErrSingleLimit.Error() {
			t.Fatalf("expected ErrSingleLimit, got %v", err)
		}

		// Adding multiset overflow
		err = s.MultiSetKV(ctx, "limit_user", map[string]string{"overflow_multi": "val"})
		if err == nil || err.Error() != ErrMultiLimit.Error() {
			t.Fatalf("expected ErrMultiLimit, got %v", err)
		}
	})

	t.Run("MultiSetKV atomicity", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()

		// If one key in multiset is invalid, nothing is written
		pairs := map[string]string{
			"valid1":      "val1",
			"invalid key": "val2",
		}
		err := s.MultiSetKV(ctx, "u_atomic", pairs)
		if err == nil {
			t.Fatal("expected multiset error on invalid key")
		}

		kv, err := s.GetKV(ctx, "u_atomic")
		if err != nil {
			t.Fatalf("unexpected GetKV error: %v", err)
		}
		if len(kv) != 0 {
			t.Errorf("expected 0 keys written on multiset error, got %d", len(kv))
		}
	})

	t.Run("Concurrent 512-limit race test", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()

		// Concurrently try to write 600 unique keys
		var wg sync.WaitGroup
		userID := "concurrent_user"
		for i := 0; i < 600; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				_ = s.SetKV(ctx, userID, fmt.Sprintf("ck_%d", idx), "v")
			}(i)
		}
		wg.Wait()

		kv, err := s.GetKV(ctx, userID)
		if err != nil {
			t.Fatalf("GetKV error: %v", err)
		}
		if len(kv) > 512 {
			t.Fatalf("key count exceeded 512 limit! count=%d", len(kv))
		}
		if len(kv) != 512 {
			t.Logf("final key count under concurrency: %d", len(kv))
		}
	})

	t.Run("API Key generation and rotation", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()

		key1, err := s.RotateKey(ctx, "api_user")
		if err != nil {
			t.Fatalf("failed to rotate key: %v", err)
		}
		if !strings.HasPrefix(key1, "lnyd_") || len(key1) != 5+32 {
			t.Fatalf("invalid key format: %s", key1)
		}

		uid, ok, err := s.UserIDForKey(ctx, key1)
		if err != nil || !ok || uid != "api_user" {
			t.Fatalf("expected api_user for key1, got uid=%s, ok=%v, err=%v", uid, ok, err)
		}

		kCheck, ok, err := s.KeyForUser(ctx, "api_user")
		if err != nil || !ok || kCheck != key1 {
			t.Fatalf("expected key1 for user, got %s, ok=%v", kCheck, ok)
		}

		// Rotate again
		key2, err := s.RotateKey(ctx, "api_user")
		if err != nil {
			t.Fatalf("failed second rotate: %v", err)
		}
		if key2 == key1 {
			t.Fatal("expected new key to be different")
		}

		// Old key must be gone
		_, ok, _ = s.UserIDForKey(ctx, key1)
		if ok {
			t.Fatal("expected old key1 to be deleted")
		}

		// New key must map to api_user
		uid, ok, _ = s.UserIDForKey(ctx, key2)
		if !ok || uid != "api_user" {
			t.Fatalf("expected api_user for key2, got %s", uid)
		}
	})
}

func TestMemoryStore(t *testing.T) {
	runStoreTests(t, func(t *testing.T) Store {
		return NewMemoryStore()
	})
}

func TestBboltStore(t *testing.T) {
	runStoreTests(t, func(t *testing.T) Store {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test_lanyard.db")
		store, err := NewBboltStore(dbPath)
		if err != nil {
			t.Fatalf("failed to create BboltStore: %v", err)
		}
		return store
	})
}
