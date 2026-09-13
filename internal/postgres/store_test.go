package postgres_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/keyno63/Shiorin/db"
	"github.com/keyno63/Shiorin/internal/auth"
	"github.com/keyno63/Shiorin/internal/bookmark"
	"github.com/keyno63/Shiorin/internal/postgres"
)

// Each run owns a randomly named schema; no existing tables are modified.
func TestPostgresPersistenceAndSessionIsolation(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "shiorin_test_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		clean, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(clean, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("cleanup test schema: %v", err)
		}
	}()
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = name
	open := func() *postgres.Store {
		t.Helper()
		pool, err := pgxpool.NewWithConfig(ctx, config.Copy())
		if err != nil {
			t.Fatal(err)
		}
		return &postgres.Store{Pool: pool}
	}
	one, two := open(), open()
	defer func() { one.Close(); two.Close() }()
	// Concurrent migration runners must not apply a migration twice.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, store := range []*postgres.Store{one, two} {
		wg.Add(1)
		go func(s *postgres.Store) { defer wg.Done(); errs <- db.Migrate(ctx, s.Pool) }(store)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := one.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
	a, b := auth.NewWithRepository(one), auth.NewWithRepository(two)
	password := "a-long-test-passphrase"
	alice, err := a.Register(ctx, "alice", password)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := b.Register(ctx, "bob", password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Register(ctx, " ALICE ", password); !errors.Is(err, auth.ErrUsernameTaken) {
		t.Fatalf("duplicate username: %v", err)
	}
	if _, err := b.SignIn(ctx, "alice", "wrong"); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatal("invalid password accepted")
	}
	laptop, err := a.SignInWithDevice(ctx, "alice", password, "Laptop", "Browser/1.0")
	if err != nil {
		t.Fatal(err)
	}
	phone, err := b.SignInWithDevice(ctx, "alice", password, "Phone", "App/1.0")
	if err != nil {
		t.Fatal(err)
	}
	bobLogin, err := b.SignIn(ctx, "bob", password)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct{ user, title string }{{alice.ID, "Go検索 one"}, {bob.ID, "private Go note"}, {alice.ID, "Go検索 two"}} {
		if _, err := one.Create(ctx, input.user, bookmark.Input{Title: input.title, URL: "https://example.com", Tags: []string{"go"}}); err != nil {
			t.Fatal(err)
		}
	}
	// Closing/reopening a pool and service simulates an API restart.
	one.Close()
	one = open()
	a = auth.NewWithRepository(one)
	p, err := a.Resolve(ctx, laptop.AccessToken)
	if err != nil || p.User.ID != alice.ID {
		t.Fatalf("session did not survive restart: %v", err)
	}
	if _, err := a.SignIn(ctx, "alice", password); err != nil {
		t.Fatal("account did not survive restart")
	}
	items, total, err := two.Search(ctx, alice.ID, bookmark.Query{Text: "検索", Tag: "go", Limit: 1, Offset: 1})
	if err != nil || total != 2 || len(items) != 1 || items[0].Title != "Go検索 one" {
		t.Fatalf("persistent scoped search: %v %d %+v", err, total, items)
	}
	items, total, err = two.Search(ctx, bob.ID, bookmark.Query{})
	if err != nil || total != 1 || len(items) != 1 || items[0].UserID != bob.ID {
		t.Fatal("bookmark isolation failed")
	}
	_, total, err = two.Search(ctx, alice.ID, bookmark.Query{Text: "' OR 1=1 --"})
	if err != nil || total != 0 {
		t.Fatal("search input treated as SQL")
	}
	_, total, err = two.Search(ctx, alice.ID, bookmark.Query{Offset: 100})
	if err != nil || total != 2 {
		t.Fatal("total lost on empty page")
	}
	if _, _, err := two.Search(ctx, "", bookmark.Query{}); !errors.Is(err, bookmark.ErrMissingUser) {
		t.Fatal("unscoped query allowed")
	}
	if err := a.Rename(ctx, p, phone.SessionID, "Work phone"); err != nil {
		t.Fatal(err)
	}
	phonePrincipal, err := b.Resolve(ctx, phone.AccessToken)
	if err != nil || phonePrincipal.Session.DeviceLabel != "Work phone" {
		t.Fatal("session rename not shared")
	}
	bobPrincipal, err := b.Resolve(ctx, bobLogin.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Revoke(ctx, bobPrincipal, phone.SessionID); !errors.Is(err, auth.ErrNotFound) {
		t.Fatal("cross-user revocation allowed")
	}
	if err := b.Rename(ctx, bobPrincipal, phone.SessionID, "stolen"); !errors.Is(err, auth.ErrNotFound) {
		t.Fatal("cross-user rename allowed")
	}
	if err := a.Revoke(ctx, p, phone.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Resolve(ctx, phone.AccessToken); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatal("revocation not shared")
	}
	if err := a.RevokeOthers(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Resolve(ctx, laptop.AccessToken); err != nil {
		t.Fatal("current session revoked")
	}
	if _, err := b.Resolve(ctx, bobLogin.AccessToken); err != nil {
		t.Fatal("bob session revoked")
	}
	sessions, err := b.Sessions(ctx, p)
	if err != nil || len(sessions) != 1 || !sessions[0].Current {
		t.Fatal("active session listing incorrect")
	}
	// Token digests are stored, while list entries expose independent session IDs.
	var stored []byte
	if err := one.Pool.QueryRow(ctx, `SELECT token_hash FROM sessions WHERE id=$1`, laptop.SessionID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256([]byte(laptop.AccessToken))
	if hex.EncodeToString(stored) != hex.EncodeToString(expected[:]) {
		t.Fatal("token hash mismatch")
	}
	old := time.Now().UTC().Add(-10 * time.Minute)
	if _, err := one.Pool.Exec(ctx, `UPDATE sessions SET last_seen_at=$2 WHERE id=$1`, laptop.SessionID, old); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Resolve(ctx, laptop.AccessToken); err != nil {
		t.Fatal(err)
	}
	var touched, again time.Time
	if err := one.Pool.QueryRow(ctx, `SELECT last_seen_at FROM sessions WHERE id=$1`, laptop.SessionID).Scan(&touched); err != nil {
		t.Fatal(err)
	}
	if !touched.After(old) {
		t.Fatal("last_seen not touched")
	}
	if _, err := a.Resolve(ctx, laptop.AccessToken); err != nil {
		t.Fatal(err)
	}
	if err := one.Pool.QueryRow(ctx, `SELECT last_seen_at FROM sessions WHERE id=$1`, laptop.SessionID).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if !again.Equal(touched) {
		t.Fatal("last_seen updated too frequently")
	}
	if _, err := one.Pool.Exec(ctx, `UPDATE sessions SET created_at=now()-interval '10 days',expires_at=now()-interval '8 days' WHERE id=$1`, laptop.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Resolve(ctx, laptop.AccessToken); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatal("expired session accepted")
	}
	if count, err := a.Cleanup(ctx); err != nil || count != 1 {
		t.Fatalf("cleanup: %d %v", count, err)
	}
	if _, err := b.Resolve(ctx, bobLogin.AccessToken); err != nil {
		t.Fatal("cleanup removed active session")
	}
	// Refuse changed migration history rather than silently accepting it.
	if _, err := one.Pool.Exec(ctx, `UPDATE shiorin_migrations SET checksum='modified' WHERE version=1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, one.Pool); err == nil {
		t.Fatal("changed migration accepted")
	}
}
