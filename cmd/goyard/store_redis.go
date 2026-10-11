//go:build redis

package main

import (
	"fmt"
	"os"

	"goyard/internal/config"
	"goyard/internal/store"
)

func initStore(cfg *config.Config) (store.Store, error) {
	if cfg.RedisURL != "" {
		return store.NewRedisStore(cfg.RedisURL)
	}
	return store.NewBboltStore(cfg.DBPath)
}

func runBackup(dest string) {
	fmt.Fprintf(os.Stderr, "backup command is not supported in Redis mode\n")
	os.Exit(1)
}
