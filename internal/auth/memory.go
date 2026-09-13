package auth

import (
	"context"
	"sort"
	"sync"
	"time"
)

type memorySession struct {
	session   Session
	revokedAt *time.Time
}

// MemoryRepository is intended for unit tests and explicit ephemeral development.
type MemoryRepository struct {
	mu       sync.Mutex
	accounts map[string]Account
	sessions map[[32]byte]memorySession
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{accounts: map[string]Account{}, sessions: map[[32]byte]memorySession{}}
}

func copyAccount(a Account) Account {
	a.Salt = append([]byte(nil), a.Salt...)
	a.Hash = append([]byte(nil), a.Hash...)
	return a
}

func (m *MemoryRepository) CreateAccount(ctx context.Context, a Account) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[a.User.Username]; ok {
		return ErrUsernameTaken
	}
	m.accounts[a.User.Username] = copyAccount(a)
	return nil
}

func (m *MemoryRepository) FindAccount(ctx context.Context, name string) (Account, error) {
	if err := ctx.Err(); err != nil {
		return Account{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[name]
	if !ok {
		return Account{}, ErrUnauthorized
	}
	return copyAccount(a), nil
}

func (m *MemoryRepository) CreateSession(ctx context.Context, key [32]byte, s Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[key] = memorySession{session: s}
	return nil
}

func (m *MemoryRepository) ResolveSession(ctx context.Context, key [32]byte, now time.Time) (Principal, error) {
	if err := ctx.Err(); err != nil {
		return Principal{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.sessions[key]
	if !ok || v.revokedAt != nil || !now.Before(v.session.ExpiresAt) {
		return Principal{}, ErrUnauthorized
	}
	if !now.Before(v.session.LastSeenAt.Add(5 * time.Minute)) {
		v.session.LastSeenAt = now
		m.sessions[key] = v
	}
	for _, a := range m.accounts {
		if a.User.ID == v.session.UserID {
			return Principal{User: a.User, Session: v.session}, nil
		}
	}
	return Principal{}, ErrUnauthorized
}

func (m *MemoryRepository) ListSessions(ctx context.Context, user string, now time.Time) ([]Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Session{}
	for _, v := range m.sessions {
		if v.session.UserID == user && v.revokedAt == nil && now.Before(v.session.ExpiresAt) {
			out = append(out, v.session)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (m *MemoryRepository) RevokeSession(ctx context.Context, user, id string, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range m.sessions {
		if v.session.UserID == user && v.session.ID == id && v.revokedAt == nil && now.Before(v.session.ExpiresAt) {
			v.revokedAt = &now
			m.sessions[k] = v
			return nil
		}
	}
	return ErrNotFound
}

func (m *MemoryRepository) RevokeOtherSessions(ctx context.Context, user, keep string, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range m.sessions {
		if v.session.UserID == user && v.session.ID != keep && v.revokedAt == nil && now.Before(v.session.ExpiresAt) {
			v.revokedAt = &now
			m.sessions[k] = v
		}
	}
	return nil
}

func (m *MemoryRepository) RenameSession(ctx context.Context, user, id, label string, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range m.sessions {
		if v.session.UserID == user && v.session.ID == id && v.revokedAt == nil && now.Before(v.session.ExpiresAt) {
			v.session.DeviceLabel = label
			m.sessions[k] = v
			return nil
		}
	}
	return ErrNotFound
}

// Keep inactive records for seven days; they never authenticate during retention.
func (m *MemoryRepository) CleanupSessions(ctx context.Context, now time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := now.Add(-7 * 24 * time.Hour)
	var count int64
	for k, v := range m.sessions {
		if !v.session.ExpiresAt.After(cutoff) || (v.revokedAt != nil && !v.revokedAt.After(cutoff)) {
			delete(m.sessions, k)
			count++
		}
	}
	return count, nil
}
