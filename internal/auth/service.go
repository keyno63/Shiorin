// Package auth provides accounts and revocable bearer sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

var (
	ErrInvalidInput  = errors.New("username must be 3-32 ASCII letters, digits, underscores or hyphens; password must be at least 15 characters and at most 1024 bytes")
	ErrUsernameTaken = errors.New("username is already registered")
	ErrUnauthorized  = errors.New("invalid credentials or session")
	ErrBusy          = errors.New("too many authentication requests; try again later")
	usernamePattern  = regexp.MustCompile(`^[a-z0-9_-]{3,32}$`)
)

const sessionTTL = 24 * time.Hour

type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

type Login struct {
	User        User      `json:"user"`
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresAt   time.Time `json:"expires_at"`
	SessionID   string    `json:"session_id"`
}

type Service struct {
	repo    Repository
	hashing chan struct{}
	now     func() time.Time
}

func New() *Service {
	return NewWithRepository(NewMemoryRepository())
}

func NewWithRepository(repo Repository) *Service {
	return &Service{repo: repo, hashing: make(chan struct{}, 2), now: time.Now}
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return b, err
}

// Limit simultaneous Argon2 allocations. Each hash uses 19 MiB, two passes,
// one lane and a 32-byte output, with an independent 16-byte salt per account.
func (s *Service) hash(ctx context.Context, password string, salt []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case s.hashing <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	defer func() { <-s.hashing }()
	hash := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return hash, nil
}

func (s *Service) Register(ctx context.Context, username, password string) (User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !usernamePattern.MatchString(username) || !utf8.ValidString(password) || utf8.RuneCountInString(password) < 15 || len(password) > 1024 {
		return User{}, ErrInvalidInput
	}
	salt, err := randomBytes(16)
	if err != nil {
		return User{}, err
	}
	hash, err := s.hash(ctx, password, salt)
	if err != nil {
		return User{}, err
	}
	id, err := randomBytes(16)
	if err != nil {
		return User{}, err
	}
	u := User{ID: base64.RawURLEncoding.EncodeToString(id), Username: username, CreatedAt: s.now().UTC()}
	if err := s.repo.CreateAccount(ctx, Account{User: u, Salt: salt, Hash: hash}); err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Service) SignIn(ctx context.Context, username, password string) (Login, error) {
	return s.SignInWithDevice(ctx, username, password, "", "")
}

func (s *Service) SignInWithDevice(ctx context.Context, username, password, label, userAgent string) (Login, error) {
	label = strings.TrimSpace(label)
	if !utf8.ValidString(label) || len(label) > 100 || strings.ContainsRune(label, 0) {
		return Login{}, ErrInvalidLabel
	}
	userAgent = strings.ReplaceAll(strings.ToValidUTF8(userAgent, ""), "\x00", "")
	if len(userAgent) > 512 {
		userAgent = strings.ToValidUTF8(userAgent[:512], "")
	}
	username = strings.ToLower(strings.TrimSpace(username))
	if !usernamePattern.MatchString(username) || len(password) > 1024 {
		return Login{}, ErrUnauthorized
	}
	a, err := s.repo.FindAccount(ctx, username)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrUnauthorized) {
		return Login{}, err
	}
	// Unknown usernames still incur the same password hashing work.
	if !exists {
		a.Salt = make([]byte, 16)
		a.Hash = make([]byte, 32)
	}
	hash, err := s.hash(ctx, password, a.Salt)
	if err != nil {
		return Login{}, err
	}
	if subtle.ConstantTimeCompare(hash, a.Hash) != 1 || !exists {
		return Login{}, ErrUnauthorized
	}
	raw, err := randomBytes(32)
	if err != nil {
		return Login{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now().UTC()
	expires := now.Add(sessionTTL)
	id, err := randomBytes(16)
	if err != nil {
		return Login{}, err
	}
	session := Session{ID: base64.RawURLEncoding.EncodeToString(id), UserID: a.User.ID, CreatedAt: now, ExpiresAt: expires, LastSeenAt: now, DeviceLabel: label, UserAgent: userAgent}
	if err := s.repo.CreateSession(ctx, sha256.Sum256([]byte(token)), session); err != nil {
		return Login{}, err
	}
	return Login{User: a.User, AccessToken: token, TokenType: "Bearer", ExpiresAt: expires, SessionID: session.ID}, nil
}

func (s *Service) Resolve(ctx context.Context, token string) (Principal, error) {
	if len(token) != 43 {
		return Principal{}, ErrUnauthorized
	}
	key := sha256.Sum256([]byte(token))
	return s.repo.ResolveSession(ctx, key, s.now().UTC())
}

func (s *Service) Sessions(ctx context.Context, p Principal) ([]Session, error) {
	items, err := s.repo.ListSessions(ctx, p.User.ID, s.now().UTC())
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Current = items[i].ID == p.Session.ID
	}
	return items, nil
}

func (s *Service) Revoke(ctx context.Context, p Principal, id string) error {
	return s.repo.RevokeSession(ctx, p.User.ID, id, s.now().UTC())
}

func (s *Service) RevokeOthers(ctx context.Context, p Principal) error {
	return s.repo.RevokeOtherSessions(ctx, p.User.ID, p.Session.ID, s.now().UTC())
}

var ErrInvalidLabel = errors.New("device_label must be valid UTF-8 without NUL and at most 100 bytes")

func (s *Service) Rename(ctx context.Context, p Principal, id, label string) error {
	label = strings.TrimSpace(label)
	if !utf8.ValidString(label) || len(label) > 100 || strings.ContainsRune(label, 0) {
		return ErrInvalidLabel
	}
	return s.repo.RenameSession(ctx, p.User.ID, id, label, s.now().UTC())
}

func (s *Service) Cleanup(ctx context.Context) (int64, error) {
	return s.repo.CleanupSessions(ctx, s.now().UTC())
}
