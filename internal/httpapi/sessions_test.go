package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/keyno63/Shiorin/internal/auth"
	"github.com/keyno63/Shiorin/internal/bookmark"
	"github.com/keyno63/Shiorin/internal/httpapi"
)

func sessionLogin(t *testing.T, h http.Handler, username, label string) auth.Login {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":"a-long-test-passphrase","device_label":%q}`, username, label)
	w := request(h, "POST", "/auth/login", body)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	var v auth.Login
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

type unavailableSessions struct{ auth.Repository }

func (unavailableSessions) ResolveSession(context.Context, [32]byte, time.Time) (auth.Principal, error) {
	return auth.Principal{}, errors.New("test database unavailable")
}

func TestSessionStoreFailureDoesNotGrantAccess(t *testing.T) {
	store := unavailableSessions{Repository: auth.NewMemoryRepository()}
	h := httpapi.New(&bookmark.Memory{}, auth.NewWithRepository(store))
	for _, path := range []string{"/bookmarks", "/me", "/me/sessions"} {
		w := withToken(h, "GET", path, "", strings.Repeat("x", 43))
		if w.Code != 500 {
			t.Fatalf("expected unavailable store to fail closed, got %d", w.Code)
		}
		if strings.Contains(w.Body.String(), "database unavailable") {
			t.Fatal("storage details exposed")
		}
	}
}

func TestSessionManagementAcrossAPIServers(t *testing.T) {
	store := auth.NewMemoryRepository()
	bookmarks := &bookmark.Memory{}
	one := httpapi.New(bookmarks, auth.NewWithRepository(store))
	two := httpapi.New(bookmarks, auth.NewWithRepository(store))
	_, first := registerAndLogin(t, one, "alice")
	_, bob := registerAndLogin(t, two, "bob")
	phone := sessionLogin(t, one, "alice", "My phone")
	w := withToken(two, "GET", "/me/sessions", "", first)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var list struct {
		Items []auth.Session `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 {
		t.Fatal("sessions leaked or missing")
	}
	current := 0
	for _, v := range list.Items {
		if v.Current {
			current++
		}
		if v.ID == phone.SessionID && v.DeviceLabel != "My phone" {
			t.Fatal("label missing")
		}
	}
	if current != 1 {
		t.Fatal("current session not identified")
	}
	if strings.Contains(w.Body.String(), first) || strings.Contains(w.Body.String(), "token_hash") {
		t.Fatal("session credentials exposed")
	}
	path := "/me/sessions/" + phone.SessionID
	for _, method := range []string{"PATCH", "DELETE"} {
		if withToken(two, method, path, `{"device_label":"stolen"}`, bob).Code != 404 {
			t.Fatal("cross-user session operation allowed")
		}
	}
	if withToken(two, "PATCH", path, `{}`, first).Code != 400 {
		t.Fatal("missing label accepted")
	}
	if withToken(two, "PATCH", path, `{"device_label":"Work phone"}`, first).Code != 204 {
		t.Fatal("rename failed")
	}
	w = withToken(one, "GET", "/me/sessions", "", phone.AccessToken)
	if !strings.Contains(w.Body.String(), "Work phone") {
		t.Fatal("rename not visible across servers")
	}
	if withToken(two, "DELETE", path, "", first).Code != 204 {
		t.Fatal("revoke failed")
	}
	if withToken(one, "GET", "/me", "", phone.AccessToken).Code != 401 {
		t.Fatal("revoked session still authenticated on other server")
	}
	other := sessionLogin(t, two, "alice", "Other browser")
	if withToken(one, "POST", "/me/sessions/revoke-others", "", first).Code != 204 {
		t.Fatal("revoke others failed")
	}
	if withToken(two, "GET", "/me", "", other.AccessToken).Code != 401 {
		t.Fatal("other session survived")
	}
	if withToken(two, "GET", "/me", "", first).Code != 200 {
		t.Fatal("current session revoked")
	}
	if withToken(one, "GET", "/me", "", bob).Code != 200 {
		t.Fatal("another user's session revoked")
	}
	for _, tc := range []struct{ method, path string }{{"GET", "/me/sessions"}, {"PATCH", path}, {"DELETE", path}, {"POST", "/me/sessions/revoke-others"}} {
		if withToken(one, tc.method, tc.path, `{}`, "").Code != 401 {
			t.Fatal("session management without authentication")
		}
	}
}
