//go:build !redis

package main

import (
	"fmt"
	"os"

	"goyard/internal/config"
	"goyard/internal/store"
)

func initStore(cfg *config.Config) (store.Store, error) {
	return store.NewBboltStore(cfg.DBPath)
}

func runBackup(dest string) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	bStore, err := store.NewBboltStore(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open db: %v\n", err)
		os.Exit(1)
	}
	defer bStore.Close()

	destFile, err := os.Create(dest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create destination backup file: %v\n", err)
		os.Exit(1)
	}
	defer destFile.Close()

	if err := bStore.Backup(destFile); err != nil {
		fmt.Fprintf(os.Stderr, "backup failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Backup saved successfully to %s\n", dest)
}
