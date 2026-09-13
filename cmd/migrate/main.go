package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/keyno63/Shiorin/db"
	"github.com/keyno63/Shiorin/internal/postgres"
)

func run() error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	store, err := postgres.Open(ctx, url)
	if err != nil {
		return err
	}
	defer store.Close()
	return db.Migrate(ctx, store.Pool)
}

func main() {
	if err := run(); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("database schema is up to date")
}
