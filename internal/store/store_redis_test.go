//go:build redis

package store

import (
	"os"
	"testing"
)

func TestRedisStore(t *testing.T) {
	redisURL := os.Getenv("REDIS_TEST_URL")
	if redisURL == "" {
		t.Skip("skipping Redis store tests: REDIS_TEST_URL not set")
	}

	runStoreTests(t, func(t *testing.T) Store {
		s, err := NewRedisStore(redisURL)
		if err != nil {
			t.Fatalf("failed to connect to redis: %v", err)
		}
		// Flush test db
		s.Client().FlushDB(t.Context())
		return s
	})
}
