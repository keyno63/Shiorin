package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPasswordsAndSessionLifecycle(t *testing.T) {
	s := New()
	m := s.repo.(*MemoryRepository)
	ctx := context.Background()
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	password := "a-long-test-passphrase"
	for _, name := range []string{"alice", "bob"} {
		if _, err := s.Register(ctx, name, password); err != nil {
			t.Fatal(err)
		}
	}
	a, b := m.accounts["alice"], m.accounts["bob"]
	if bytes.Equal(a.Hash, []byte(password)) || bytes.Equal(a.Hash, b.Hash) || bytes.Equal(a.Salt, b.Salt) {
		t.Fatal("passwords must have independently salted hashes")
	}
	first, err := s.SignIn(ctx, "alice", password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.SignIn(ctx, "alice", password)
	if err != nil {
		t.Fatal(err)
	}
	if first.AccessToken == second.AccessToken {
		t.Fatal("tokens must be independent")
	}
	if _, ok := m.sessions[sha256.Sum256([]byte(first.AccessToken))]; !ok {
		t.Fatal("session token should be stored by digest")
	}
	p, err := s.Resolve(ctx, first.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, p, p.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(ctx, first.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked token accepted")
	}
	if _, err := s.Resolve(ctx, second.AccessToken); err != nil {
		t.Fatal(err)
	}
	now = second.ExpiresAt
	if _, err := s.Resolve(ctx, second.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired token accepted")
	}
	now = now.Add(7 * 24 * time.Hour)
	if _, err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if len(m.sessions) != 0 {
		t.Fatal("expired session retained")
	}
}

func TestPasswordBoundsAndCancelledRequests(t *testing.T) {
	s := New()
	m := s.repo.(*MemoryRepository)
	ctx := context.Background()
	for _, password := range []string{"short", strings.Repeat("x", 1025)} {
		if _, err := s.Register(ctx, "alice", password); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid password accepted")
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Register(ctx, "alice", "a-long-test-passphrase"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled registration accepted")
	}
	if len(m.accounts) != 0 {
		t.Fatal("cancelled request created an account")
	}
}
