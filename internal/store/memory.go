package store

import (
	"context"
	"maps"
	"sync"
)

type MemoryStore struct {
	mu       sync.RWMutex
	kv       map[string]map[string]string // userID -> key -> value
	apiKeys  map[string]string            // apiKey -> userID
	userKeys map[string]string            // userID -> apiKey
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		kv:       make(map[string]map[string]string),
		apiKeys:  make(map[string]string),
		userKeys: make(map[string]string),
	}
}

func (m *MemoryStore) GetKV(ctx context.Context, userID string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	userKV, ok := m.kv[userID]
	if !ok {
		return make(map[string]string), nil
	}
	res := make(map[string]string, len(userKV))
	maps.Copy(res, userKV)
	return res, nil
}

func (m *MemoryStore) SetKV(ctx context.Context, userID, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	userKV, ok := m.kv[userID]
	if !ok {
		userKV = make(map[string]string)
		m.kv[userID] = userKV
	}

	// 1. Key-count limit using merged key set
	_, exists := userKV[key]
	if !exists && len(userKV) >= 512 {
		return ErrSingleLimit
	}

	// 2. Validate key/value
	if err := ValidateKey(key); err != nil {
		return err
	}
	if err := ValidateValue(value); err != nil {
		return err
	}

	userKV[key] = value
	return nil
}

func (m *MemoryStore) MultiSetKV(ctx context.Context, userID string, pairs map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. Validate every pair first
	for k, v := range pairs {
		if err := ValidateKey(k); err != nil {
			return err
		}
		if err := ValidateValue(v); err != nil {
			return err
		}
	}

	userKV, ok := m.kv[userID]
	if !ok {
		userKV = make(map[string]string)
		m.kv[userID] = userKV
	}

	// 2. Key-count limit on the merged set
	mergedCount := len(userKV)
	for k := range pairs {
		if _, exists := userKV[k]; !exists {
			mergedCount++
		}
	}
	if mergedCount > 512 {
		return ErrMultiLimit
	}

	// 3. Write all
	for k, v := range pairs {
		userKV[k] = v
	}
	return nil
}

func (m *MemoryStore) DelKV(ctx context.Context, userID, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if userKV, ok := m.kv[userID]; ok {
		delete(userKV, key)
	}
	return nil
}

func (m *MemoryStore) UserIDForKey(ctx context.Context, key string) (string, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	uid, ok := m.apiKeys[key]
	return uid, ok, nil
}

func (m *MemoryStore) KeyForUser(ctx context.Context, userID string) (string, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	k, ok := m.userKeys[userID]
	return k, ok, nil
}

func (m *MemoryStore) RotateKey(ctx context.Context, userID string) (string, error) {
	newKey, err := GenerateAPIKey()
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if oldKey, ok := m.userKeys[userID]; ok {
		delete(m.apiKeys, oldKey)
	}

	m.userKeys[userID] = newKey
	m.apiKeys[newKey] = userID
	return newKey, nil
}

func (m *MemoryStore) Close() error {
	return nil
}
