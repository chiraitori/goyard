package presence

import (
	"context"
	"encoding/json"
	"sync"

	"goyard/internal/hub"
	"goyard/internal/metrics"
	"goyard/internal/store"
)

type User struct {
	ID              string
	DiscordUser     map[string]any
	DiscordPresence map[string]any
	KV              map[string]string

	prettyJSON []byte
	mu         sync.RWMutex
}

type Patch struct {
	DiscordUser     map[string]any
	DiscordPresence map[string]any
	KV              map[string]string
}

type Manager struct {
	mu    sync.RWMutex
	users map[string]*User
	hub   *hub.Hub
	store store.Store
}

func NewManager(h *hub.Hub, s store.Store) *Manager {
	return &Manager{
		users: make(map[string]*User),
		hub:   h,
		store: s,
	}
}

func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.users)
}

func (m *Manager) Get(id string) ([]byte, bool) {
	m.mu.RLock()
	u, ok := m.users[id]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}

	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.prettyJSON, true
}

func (m *Manager) Upsert(id string, user map[string]any, discordPresence map[string]any) {
	m.mu.RLock()
	existingUser, exists := m.users[id]
	m.mu.RUnlock()

	if exists {
		// Update existing user
		existingUser.mu.Lock()
		if user != nil {
			existingUser.DiscordUser = user
		}
		if discordPresence != nil {
			existingUser.DiscordPresence = discordPresence
		}
		pretty := BuildPretty(existingUser.DiscordUser, existingUser.DiscordPresence, existingUser.KV)
		prettyBytes, _ := json.Marshal(pretty)
		existingUser.prettyJSON = prettyBytes
		existingUser.mu.Unlock()

		m.hub.Publish(id, prettyBytes)
		return
	}

	// New user: load KV from store outside global lock
	var kv map[string]string
	if m.store != nil {
		if kvs, err := m.store.GetKV(context.Background(), id); err == nil {
			kv = kvs
		}
	}
	if kv == nil {
		kv = make(map[string]string)
	}

	pretty := BuildPretty(user, discordPresence, kv)
	prettyBytes, _ := json.Marshal(pretty)

	newUser := &User{
		ID:              id,
		DiscordUser:     user,
		DiscordPresence: discordPresence,
		KV:              kv,
		prettyJSON:      prettyBytes,
	}

	m.mu.Lock()
	m.users[id] = newUser
	metrics.DefaultMetrics.MonitoredUsers.Store(int64(len(m.users)))
	m.mu.Unlock()

	// Notify global subscribers only on first creation
	m.hub.PublishGlobalOnly(id, prettyBytes)
}

func (m *Manager) Sync(id string, patch Patch, fromGlobalSync bool) {
	m.mu.RLock()
	u, exists := m.users[id]
	m.mu.RUnlock()

	if !exists {
		return
	}

	u.mu.Lock()
	if patch.DiscordUser != nil {
		u.DiscordUser = patch.DiscordUser
	}
	if patch.DiscordPresence != nil {
		u.DiscordPresence = patch.DiscordPresence
	}
	if patch.KV != nil {
		u.KV = patch.KV
	}

	pretty := BuildPretty(u.DiscordUser, u.DiscordPresence, u.KV)
	prettyBytes, _ := json.Marshal(pretty)
	u.prettyJSON = prettyBytes
	u.mu.Unlock()

	m.hub.Publish(id, prettyBytes)
}

func (m *Manager) Remove(id string) {
	m.mu.Lock()
	delete(m.users, id)
	metrics.DefaultMetrics.MonitoredUsers.Store(int64(len(m.users)))
	m.mu.Unlock()
}

func (m *Manager) SnapshotAll() map[string]json.RawMessage {
	m.mu.RLock()
	users := make([]*User, 0, len(m.users))
	for _, u := range m.users {
		users = append(users, u)
	}
	m.mu.RUnlock()

	res := make(map[string]json.RawMessage, len(users))
	for _, u := range users {
		u.mu.RLock()
		if len(u.prettyJSON) > 0 {
			res[u.ID] = json.RawMessage(u.prettyJSON)
		}
		u.mu.RUnlock()
	}
	return res
}
