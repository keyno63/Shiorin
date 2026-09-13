package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

func TestLastSeenRetentionAndMetadata(t *testing.T) {
	m := NewMemoryRepository()
	s := NewWithRepository(m)
	ctx := context.Background()
	now := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if _, err := s.Register(ctx, "alice", "a-long-test-passphrase"); err != nil {
		t.Fatal(err)
	}
	login, err := s.SignInWithDevice(ctx, "alice", "a-long-test-passphrase", " My laptop ", "Browser/1.0")
	if err != nil {
		t.Fatal(err)
	}
	initial := now
	now = now.Add(time.Minute)
	p, err := s.Resolve(ctx, login.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Session.LastSeenAt.Equal(initial) || p.Session.DeviceLabel != "My laptop" || p.Session.UserAgent != "Browser/1.0" {
		t.Fatal("unexpected session metadata")
	}
	now = initial.Add(5 * time.Minute)
	p, err = s.Resolve(ctx, login.AccessToken)
	if err != nil || !p.Session.LastSeenAt.Equal(now) {
		t.Fatal("last seen was not updated")
	}
	if err := s.Revoke(ctx, p, p.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(ctx, login.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked session authenticates")
	}
	key := sha256.Sum256([]byte(login.AccessToken))
	if _, ok := m.sessions[key]; !ok {
		t.Fatal("revoked record should remain during retention")
	}
	now = now.Add(7 * 24 * time.Hour)
	n, err := s.Cleanup(ctx)
	if err != nil || n != 1 {
		t.Fatalf("cleanup: %d %v", n, err)
	}
}
