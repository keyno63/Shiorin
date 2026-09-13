package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/keyno63/Shiorin/internal/auth"
	"github.com/keyno63/Shiorin/internal/bookmark"
)

func New(repo bookmark.Repository, accounts *auth.Service) http.Handler {
	mux := http.NewServeMux()
	addAuthRoutes(mux, accounts)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /bookmarks", requireUser(accounts, func(w http.ResponseWriter, r *http.Request, user auth.User) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var in bookmark.Input
		if err := dec.Decode(&in); err != nil {
			badJSON(w, err)
			return
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			badJSON(w, err)
			return
		}
		in.Title = strings.TrimSpace(in.Title)
		in.URL = strings.TrimSpace(in.URL)
		if strings.ContainsRune(in.Title+in.URL+in.Note+strings.Join(in.Tags, ""), 0) {
			fail(w, 400, "bookmark fields must not contain NUL characters")
			return
		}
		if in.Title == "" || len(in.Title) > 500 {
			fail(w, 400, "title is required and must be at most 500 bytes")
			return
		}
		u, err := url.Parse(in.URL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
			fail(w, 400, "url must be an absolute http or https URL")
			return
		}
		tags := []string{}
		seen := map[string]bool{}
		for _, t := range in.Tags {
			t = strings.ToLower(strings.TrimSpace(t))
			if t != "" && !seen[t] {
				tags = append(tags, t)
				seen[t] = true
			}
		}
		in.Tags = tags
		b, err := repo.Create(r.Context(), user.ID, in)
		if err != nil {
			slog.Error("create bookmark", "error", err)
			fail(w, 500, "internal server error")
			return
		}
		respond(w, 201, b)
	}))
	mux.HandleFunc("GET /bookmarks", requireUser(accounts, func(w http.ResponseWriter, r *http.Request, user auth.User) {
		if r.URL.Query().Has("user_id") {
			fail(w, 400, "user_id is determined by your session")
			return
		}
		limit, err := number(r, "limit", 20, 1, 100)
		if err != nil {
			fail(w, 400, "limit must be between 1 and 100")
			return
		}
		offset, err := number(r, "offset", 0, 0, 1000000)
		if err != nil {
			fail(w, 400, "offset must be between 0 and 1000000")
			return
		}
		q := bookmark.Query{Text: r.URL.Query().Get("q"), Tag: r.URL.Query().Get("tag"), Limit: limit, Offset: offset}
		if strings.ContainsRune(q.Text+q.Tag, 0) {
			fail(w, 400, "search fields must not contain NUL characters")
			return
		}
		items, total, err := repo.Search(r.Context(), user.ID, q)
		if err != nil {
			slog.Error("search bookmarks", "error", err)
			fail(w, 500, "internal server error")
			return
		}
		respond(w, 200, struct {
			Items  []bookmark.Bookmark `json:"items"`
			Total  int                 `json:"total"`
			Limit  int                 `json:"limit"`
			Offset int                 `json:"offset"`
		}{items, total, limit, offset})
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

func number(r *http.Request, key string, fallback, min, max int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return 0, errors.New("out of range")
	}
	return n, nil
}

func badJSON(w http.ResponseWriter, err error) {
	var large *http.MaxBytesError
	if errors.As(err, &large) {
		fail(w, 413, "body exceeds 1 MiB")
		return
	}
	fail(w, 400, "body must contain one valid JSON object with known fields")
}
func fail(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]string{"error": message})
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("write response", "error", err)
	}
}
