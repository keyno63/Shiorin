package auth

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("session not found")

// Account is internal credential storage. Never serialize it to an API response.
type Account struct {
	User       User
	Salt, Hash []byte
}

type Session struct {
	ID          string    `json:"id"`
	UserID      string    `json:"-"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	DeviceLabel string    `json:"device_label"`
	UserAgent   string    `json:"user_agent"`
	Current     bool      `json:"current"`
}

type Principal struct {
	User    User
	Session Session
}

// Repository implementations must scope all session mutations by user ID.
// ResolveSession checks expiry/revocation every time and touches last_seen_at
// at most once every five minutes; expired/revoked records are never revived.
type Repository interface {
	CreateAccount(context.Context, Account) error
	FindAccount(context.Context, string) (Account, error)
	CreateSession(context.Context, [32]byte, Session) error
	ResolveSession(context.Context, [32]byte, time.Time) (Principal, error)
	ListSessions(context.Context, string, time.Time) ([]Session, error)
	RevokeSession(context.Context, string, string, time.Time) error
	RevokeOtherSessions(context.Context, string, string, time.Time) error
	RenameSession(context.Context, string, string, string, time.Time) error
	CleanupSessions(context.Context, time.Time) (int64, error)
}
