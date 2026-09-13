// Package postgres persists accounts, sessions and private bookmarks.
package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/keyno63/Shiorin/internal/auth"
	"github.com/keyno63/Shiorin/internal/bookmark"
)

type Store struct{ Pool *pgxpool.Pool }

var _ auth.Repository = (*Store)(nil)
var _ bookmark.Repository = (*Store)(nil)

func Open(ctx context.Context, url string) (*Store, error) {
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL")
	}
	config.MaxConns = 10
	config.ConnConfig.ConnectTimeout = 5 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("cannot initialize PostgreSQL pool")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("cannot connect to PostgreSQL; check DATABASE_URL and server availability")
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Close() { s.Pool.Close() }

func (s *Store) CreateAccount(ctx context.Context, a auth.Account) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO users(id,username,password_salt,password_hash,created_at) VALUES($1,$2,$3,$4,$5)`, a.User.ID, a.User.Username, a.Salt, a.Hash, a.User.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "users_username_key" {
		return auth.ErrUsernameTaken
	}
	return err
}

func (s *Store) FindAccount(ctx context.Context, name string) (auth.Account, error) {
	var a auth.Account
	var algorithm string
	var version, memory, iterations, lanes int
	err := s.Pool.QueryRow(ctx, `SELECT id,username,created_at,password_salt,password_hash,password_algorithm,password_version,password_memory_kib,password_iterations,password_parallelism FROM users WHERE username=$1`, name).Scan(&a.User.ID, &a.User.Username, &a.User.CreatedAt, &a.Salt, &a.Hash, &algorithm, &version, &memory, &iterations, &lanes)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, auth.ErrUnauthorized
	}
	if err != nil {
		return a, err
	}
	if algorithm != "argon2id" || version != 19 || memory != 19456 || iterations != 2 || lanes != 1 {
		return a, errors.New("unsupported stored password parameters")
	}
	return a, nil
}

func (s *Store) CreateSession(ctx context.Context, key [32]byte, v auth.Session) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO sessions(id,token_hash,user_id,created_at,expires_at,last_seen_at,device_label,user_agent) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, v.ID, key[:], v.UserID, v.CreatedAt, v.ExpiresAt, v.LastSeenAt, v.DeviceLabel, v.UserAgent)
	return err
}

func (s *Store) ResolveSession(ctx context.Context, key [32]byte, now time.Time) (auth.Principal, error) {
	var p auth.Principal
	err := s.Pool.QueryRow(ctx, `SELECT u.id,u.username,u.created_at,s.id,s.created_at,s.expires_at,s.last_seen_at,s.device_label,s.user_agent FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>$2`, key[:], now).Scan(&p.User.ID, &p.User.Username, &p.User.CreatedAt, &p.Session.ID, &p.Session.CreatedAt, &p.Session.ExpiresAt, &p.Session.LastSeenAt, &p.Session.DeviceLabel, &p.Session.UserAgent)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, auth.ErrUnauthorized
	}
	if err != nil {
		return p, err
	}
	p.Session.UserID = p.User.ID
	if !now.Before(p.Session.LastSeenAt.Add(5 * time.Minute)) {
		// The predicate limits writes even when several API servers touch concurrently.
		_, err = s.Pool.Exec(ctx, `UPDATE sessions SET last_seen_at=$2 WHERE id=$1 AND revoked_at IS NULL AND expires_at>$2 AND last_seen_at<=$3`, p.Session.ID, now, now.Add(-5*time.Minute))
		if err != nil {
			return p, err
		}
		p.Session.LastSeenAt = now
	}
	return p, nil
}

func (s *Store) ListSessions(ctx context.Context, user string, now time.Time) ([]auth.Session, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,user_id,created_at,expires_at,last_seen_at,device_label,user_agent FROM sessions WHERE user_id=$1 AND revoked_at IS NULL AND expires_at>$2 ORDER BY created_at DESC,id DESC`, user, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []auth.Session{}
	for rows.Next() {
		var v auth.Session
		if err := rows.Scan(&v.ID, &v.UserID, &v.CreatedAt, &v.ExpiresAt, &v.LastSeenAt, &v.DeviceLabel, &v.UserAgent); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) RevokeSession(ctx context.Context, user, id string, now time.Time) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE sessions SET revoked_at=$3 WHERE user_id=$1 AND id=$2 AND revoked_at IS NULL AND expires_at>$3`, user, id, now)
	if err == nil && tag.RowsAffected() == 0 {
		return auth.ErrNotFound
	}
	return err
}

func (s *Store) RevokeOtherSessions(ctx context.Context, user, keep string, now time.Time) error {
	_, err := s.Pool.Exec(ctx, `UPDATE sessions SET revoked_at=$3 WHERE user_id=$1 AND id<>$2 AND revoked_at IS NULL AND expires_at>$3`, user, keep, now)
	return err
}

func (s *Store) RenameSession(ctx context.Context, user, id, label string, now time.Time) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE sessions SET device_label=$3 WHERE user_id=$1 AND id=$2 AND revoked_at IS NULL AND expires_at>$4`, user, id, label, now)
	if err == nil && tag.RowsAffected() == 0 {
		return auth.ErrNotFound
	}
	return err
}

func (s *Store) CleanupSessions(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at<=$1 OR revoked_at<=$1`, now.Add(-7*24*time.Hour))
	return tag.RowsAffected(), err
}

func (s *Store) Create(ctx context.Context, user string, in bookmark.Input) (bookmark.Bookmark, error) {
	if strings.TrimSpace(user) == "" {
		return bookmark.Bookmark{}, bookmark.ErrMissingUser
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return bookmark.Bookmark{}, err
	}
	if in.Tags == nil {
		in.Tags = []string{}
	}
	b := bookmark.Bookmark{Input: in, UserID: user, ID: hex.EncodeToString(id[:])}
	err := s.Pool.QueryRow(ctx, `INSERT INTO bookmarks(id,user_id,title,url,note,tags) VALUES($1,$2,$3,$4,$5,$6) RETURNING created_at`, b.ID, user, in.Title, in.URL, in.Note, in.Tags).Scan(&b.CreatedAt)
	return b, err
}

func (s *Store) Search(ctx context.Context, user string, q bookmark.Query) ([]bookmark.Bookmark, int, error) {
	if strings.TrimSpace(user) == "" {
		return nil, 0, bookmark.ErrMissingUser
	}
	if q.Limit <= 0 {
		q.Limit = 20
	}
	if q.Limit > 100 {
		q.Limit = 100
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	text, tag := strings.ToLower(strings.TrimSpace(q.Text)), strings.ToLower(strings.TrimSpace(q.Tag))
	// One snapshot keeps total and page consistent while other requests write.
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)
	const filter = ` WHERE user_id=$1 AND ($2='' OR strpos(lower(title||' '||url||' '||note),$2)>0) AND ($3='' OR $3=ANY(tags))`
	var total int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM bookmarks`+filter, user, text, tag).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(ctx, `SELECT id,user_id,title,url,note,tags,created_at FROM bookmarks`+filter+` ORDER BY created_at DESC,id DESC LIMIT $4 OFFSET $5`, user, text, tag, q.Limit, q.Offset)
	if err != nil {
		return nil, 0, err
	}
	out := []bookmark.Bookmark{}
	for rows.Next() {
		var b bookmark.Bookmark
		if err := rows.Scan(&b.ID, &b.UserID, &b.Title, &b.URL, &b.Note, &b.Tags, &b.CreatedAt); err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func (s *Store) CheckSchema(ctx context.Context) error {
	var version int
	if err := s.Pool.QueryRow(ctx, `SELECT coalesce(max(version),0) FROM shiorin_migrations`).Scan(&version); err != nil {
		return errors.New("database schema is not initialized; run go run ./cmd/migrate")
	}
	if version != 1 {
		return fmt.Errorf("unsupported database schema version %d", version)
	}
	return nil
}
