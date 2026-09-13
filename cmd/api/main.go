package main

import (
	"context"
	"errors"
	"github.com/keyno63/Shiorin/internal/auth"
	"github.com/keyno63/Shiorin/internal/bookmark"
	"github.com/keyno63/Shiorin/internal/httpapi"
	"github.com/keyno63/Shiorin/internal/postgres"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	mode := os.Getenv("STORAGE")
	if mode == "" {
		mode = "postgres"
	}
	var bookmarks bookmark.Repository
	var accounts *auth.Service
	switch mode {
	case "memory":
		bookmarks = &bookmark.Memory{}
		accounts = auth.New()
	case "postgres":
		url := os.Getenv("DATABASE_URL")
		if url == "" {
			return errors.New("DATABASE_URL is required (or explicitly set STORAGE=memory for ephemeral development)")
		}
		initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		store, err := postgres.Open(initCtx, url)
		if err != nil {
			cancel()
			return err
		}
		defer store.Close()
		err = store.CheckSchema(initCtx)
		cancel()
		if err != nil {
			return err
		}
		bookmarks = store
		accounts = auth.NewWithRepository(store)
	default:
		return errors.New("STORAGE must be postgres or memory")
	}
	cleanupCtx, cancelCleanup := context.WithCancel(ctx)
	cleanupDone := make(chan struct{})
	go func() { defer close(cleanupDone); cleanupSessions(cleanupCtx, accounts) }()
	defer func() { cancelCleanup(); <-cleanupDone }()
	srv := &http.Server{Addr: addr, Handler: httpapi.New(bookmarks, accounts), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	slog.Info("Shiorin starting", "addr", addr, "storage", mode)
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			_ = srv.Close()
			return err
		}
		return nil
	}
}

func cleanupSessions(ctx context.Context, accounts *auth.Service) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		op, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := accounts.Cleanup(op)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Error("session cleanup failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
