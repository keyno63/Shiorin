// Package db owns the versioned database schema.
package db

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

// Migrate serializes concurrent runners and commits schema plus version together.
// An existing unversioned schema is deliberately rejected rather than replaced.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(736846291)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS shiorin_migrations(version integer PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	var latest int
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(version),0) FROM shiorin_migrations`).Scan(&latest); err != nil {
		return err
	}
	if latest > 1 {
		return fmt.Errorf("database migration version %d is newer than this binary", latest)
	}
	// Git may check out CRLF on Windows and LF on Linux.
	canonicalSchema := strings.ReplaceAll(schema, "\r\n", "\n")
	hash := sha256.Sum256([]byte(canonicalSchema))
	checksum := hex.EncodeToString(hash[:])
	var stored string
	err = tx.QueryRow(ctx, `SELECT checksum FROM shiorin_migrations WHERE version=1`).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, canonicalSchema); err != nil {
			return fmt.Errorf("apply migration 1 (use a fresh database for this first managed schema): %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO shiorin_migrations(version,checksum) VALUES(1,$1)`, checksum); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if stored != checksum {
		return errors.New("migration 1 checksum mismatch; restore the applied migration and add a new version")
	}
	return tx.Commit(ctx)
}
