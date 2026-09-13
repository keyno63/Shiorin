package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/keyno63/Shiorin/internal/auth"
)

type credentials struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DeviceLabel string `json:"device_label"`
}

type principalKey struct{}

func principalFrom(r *http.Request) auth.Principal {
	return r.Context().Value(principalKey{}).(auth.Principal)
}

func readCredentials(w http.ResponseWriter, r *http.Request) (credentials, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	var in credentials
	if err := d.Decode(&in); err != nil {
		badJSON(w, err)
		return in, false
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		badJSON(w, err)
		return in, false
	}
	return in, true
}

func tokenFrom(r *http.Request) string {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return ""
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	fail(w, http.StatusUnauthorized, "authentication required or invalid credentials")
}

func requireUser(accounts *auth.Service, next func(http.ResponseWriter, *http.Request, auth.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		p, err := accounts.Resolve(r.Context(), tokenFrom(r))
		if err != nil {
			authError(w, err)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
		next(w, r, p.User)
	}
}

func authError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidInput):
		fail(w, 400, err.Error())
	case errors.Is(err, auth.ErrInvalidLabel):
		fail(w, 400, err.Error())
	case errors.Is(err, auth.ErrNotFound):
		fail(w, 404, "session not found")
	case errors.Is(err, auth.ErrUsernameTaken):
		fail(w, 409, err.Error())
	case errors.Is(err, auth.ErrUnauthorized):
		unauthorized(w)
	case errors.Is(err, auth.ErrBusy):
		w.Header().Set("Retry-After", "1")
		fail(w, 429, err.Error())
	default:
		slog.Error("authentication operation failed", "error", err)
		fail(w, 500, "internal server error")
	}
}

func addAuthRoutes(mux *http.ServeMux, accounts *auth.Service) {
	mux.HandleFunc("POST /auth/register", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		in, ok := readCredentials(w, r)
		if !ok {
			return
		}
		u, err := accounts.Register(r.Context(), in.Username, in.Password)
		if err != nil {
			authError(w, err)
			return
		}
		respond(w, http.StatusCreated, u)
	})
	mux.HandleFunc("POST /auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		in, ok := readCredentials(w, r)
		if !ok {
			return
		}
		login, err := accounts.SignInWithDevice(r.Context(), in.Username, in.Password, in.DeviceLabel, r.UserAgent())
		if err != nil {
			authError(w, err)
			return
		}
		respond(w, http.StatusOK, login)
	})
	mux.HandleFunc("GET /me", requireUser(accounts, func(w http.ResponseWriter, r *http.Request, u auth.User) { respond(w, http.StatusOK, u) }))
	mux.HandleFunc("POST /auth/logout", requireUser(accounts, func(w http.ResponseWriter, r *http.Request, u auth.User) {
		p := principalFrom(r)
		if err := accounts.Revoke(r.Context(), p, p.Session.ID); err != nil {
			authError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
}
