package config

import (
	"os"
	"testing"
)

func TestConfigLoad(t *testing.T) {
	os.Unsetenv("BOT_TOKEN")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error when BOT_TOKEN is missing")
	}

	os.Setenv("BOT_TOKEN", "fake_token")
	defer os.Unsetenv("BOT_TOKEN")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != 4001 {
		t.Errorf("expected default port 4001, got %d", cfg.Port)
	}
	if cfg.BotPresence != "you <3" {
		t.Errorf("expected default bot presence 'you <3', got %s", cfg.BotPresence)
	}
}

func TestConfigIdempotency(t *testing.T) {
	c := &Config{BotIdempotencyEnvKey: ""}
	if !c.IsIdempotent() {
		t.Error("expected idempotent when key empty")
	}

	c = &Config{BotIdempotencyEnvKey: "TEST_FOO=bar"}
	os.Setenv("TEST_FOO", "baz")
	if c.IsIdempotent() {
		t.Error("expected not idempotent when env does not match")
	}
	os.Setenv("TEST_FOO", "bar")
	if !c.IsIdempotent() {
		t.Error("expected idempotent when env matches")
	}
	os.Unsetenv("TEST_FOO")

	c = &Config{BotIdempotencyEnvKey: "MALFORMED"}
	if c.IsIdempotent() {
		t.Error("expected false for malformed key")
	}
}
