package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"
)

var (
	ErrKeyTooLong   = errors.New("key must be 255 characters or less")
	ErrKeyInvalid   = errors.New("key must be alphanumeric (a-zA-Z0-9_)")
	ErrValueTooLong = errors.New("value must be 30000 characters or less")
	ErrSingleLimit  = errors.New("request would exceed key limit (512), please delete keys first")
	ErrMultiLimit   = errors.New("request would exceed the key limit (512), please delete some keys first")
)

var keyRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// ValidateKey checks key validity according to section 6.2
func ValidateKey(key string) error {
	if len(key) == 0 {
		return ErrKeyInvalid
	}
	if utf8.RuneCountInString(key) > 255 {
		return ErrKeyTooLong
	}
	if !keyRegex.MatchString(key) {
		return ErrKeyInvalid
	}
	return nil
}

// ValidateValue checks value length according to section 6.2
func ValidateValue(value string) error {
	if utf8.RuneCountInString(value) > 30000 {
		return ErrValueTooLong
	}
	return nil
}

// GenerateAPIKey generates a new lnyd_ + 32 lowercase hex chars key
func GenerateAPIKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return "lnyd_" + hex.EncodeToString(b[:]), nil
}

type Store interface {
	// KV
	GetKV(ctx context.Context, userID string) (map[string]string, error)
	SetKV(ctx context.Context, userID, key, value string) error                   // enforces limits atomically
	MultiSetKV(ctx context.Context, userID string, pairs map[string]string) error // atomic, all-or-nothing
	DelKV(ctx context.Context, userID, key string) error                          // deleting a missing key is not an error

	// API keys
	UserIDForKey(ctx context.Context, key string) (userID string, ok bool, err error)
	KeyForUser(ctx context.Context, userID string) (key string, ok bool, err error)
	RotateKey(ctx context.Context, userID string) (newKey string, err error) // deletes old mapping, creates new

	Close() error
}
