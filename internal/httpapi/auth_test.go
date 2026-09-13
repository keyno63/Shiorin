package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keyno63/Shiorin/internal/auth"
	"github.com/keyno63/Shiorin/internal/bookmark"
	"github.com/keyno63/Shiorin/internal/httpapi"
)

func withToken(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func registerAndLogin(t *testing.T, h http.Handler, username string) (auth.User, string) {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":"a-long-test-passphrase"}`, username)
	w := request(h, "POST", "/auth/register", body)
	if w.Code != 201 {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "hash") {
		t.Fatal("credential data exposed")
	}
	w = request(h, "POST", "/auth/login", body)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	var login auth.Login
	if err := json.Unmarshal(w.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	if login.AccessToken == "" || login.User.ID == "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("invalid login response")
	}
	return login.User, login.AccessToken
}

func TestUsersCannotReadOrCreateEachOthersBookmarks(t *testing.T) {
	h := httpapi.New(&bookmark.Memory{}, auth.New())
	alice, aliceToken := registerAndLogin(t, h, "alice")
	bob, bobToken := registerAndLogin(t, h, "bob")
	for _, tc := range []struct{ token, title string }{{aliceToken, "Alice Go one"}, {bobToken, "Bob Go secret"}, {aliceToken, "Alice Go two"}} {
		body := fmt.Sprintf(`{"title":%q,"url":"https://example.com","tags":["go"]}`, tc.title)
		w := withToken(h, "POST", "/bookmarks", body, tc.token)
		if w.Code != 201 {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
		var b bookmark.Bookmark
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		want := alice.ID
		if tc.token == bobToken {
			want = bob.ID
		}
		if b.UserID != want {
			t.Fatal("wrong owner")
		}
	}
	for _, tc := range []struct {
		token, path  string
		total, count int
		owner, title string
	}{
		{aliceToken, "/bookmarks", 2, 2, alice.ID, "Alice Go two"},
		{bobToken, "/bookmarks?q=go&tag=go", 1, 1, bob.ID, "Bob Go secret"},
		{aliceToken, "/bookmarks?q=go&tag=go&offset=1&limit=1", 2, 1, alice.ID, "Alice Go one"},
		{aliceToken, "/bookmarks?q=secret", 0, 0, alice.ID, ""},
		{bobToken, "/bookmarks?offset=1", 1, 0, bob.ID, ""},
	} {
		w := withToken(h, "GET", tc.path, "", tc.token)
		if w.Code != 200 {
			t.Fatalf("search: %d %s", w.Code, w.Body.String())
		}
		var result struct {
			Items []bookmark.Bookmark `json:"items"`
			Total int                 `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Total != tc.total || len(result.Items) != tc.count {
			t.Fatalf("scope/pagination: %s", w.Body.String())
		}
		for _, b := range result.Items {
			if b.UserID != tc.owner {
				t.Fatal("cross-user data leak")
			}
		}
		if tc.count > 0 && result.Items[0].Title != tc.title {
			t.Fatal("wrong ordering")
		}
	}
	_, emptyToken := registerAndLogin(t, h, "empty")
	w := withToken(h, "GET", "/bookmarks", "", emptyToken)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatal("new user can see existing bookmarks")
	}
	w = withToken(h, "GET", "/bookmarks?user_id="+bob.ID, "", aliceToken)
	if w.Code != 400 {
		t.Fatal("owner query must be rejected")
	}
	w = withToken(h, "POST", "/bookmarks", fmt.Sprintf(`{"title":"spoof","url":"https://example.com","user_id":%q}`, bob.ID), aliceToken)
	if w.Code != 400 {
		t.Fatal("owner injection must be rejected")
	}
	for _, method := range []string{"GET", "POST"} {
		for _, token := range []string{"", "forged-token", bob.ID} {
			w = withToken(h, method, "/bookmarks", `{}`, token)
			if w.Code != 401 {
				t.Fatal("unauthenticated bookmark access")
			}
		}
	}
	w = withToken(h, "POST", "/auth/logout", "", aliceToken)
	if w.Code != 204 {
		t.Fatal("logout failed")
	}
	for _, path := range []string{"/me", "/bookmarks"} {
		if withToken(h, "GET", path, "", aliceToken).Code != 401 {
			t.Fatal("revoked token accepted")
		}
	}
	if withToken(h, "GET", "/me", "", bobToken).Code != 200 {
		t.Fatal("logout affected another user")
	}
}

func TestRegistrationAndLoginErrors(t *testing.T) {
	h := httpapi.New(&bookmark.Memory{}, auth.New())
	registerAndLogin(t, h, "alice")
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/auth/register", `{"username":" ALICE ","password":"a-long-test-passphrase"}`, 409},
		{"/auth/register", `{"username":"bob","password":"short"}`, 400},
		{"/auth/register", `{"username":"bad name","password":"a-long-test-passphrase"}`, 400},
		{"/auth/register", `{"username":"bob","password":"a-long-test-passphrase","id":"spoof"}`, 400},
		{"/auth/register", `{} {}`, 400},
		{"/auth/login", `{"username":"alice","password":"wrong"}`, 401},
		{"/auth/login", `{"username":"unknown","password":"wrong"}`, 401},
		{"/auth/login", `{"username":" ALICE ","password":"a-long-test-passphrase"}`, 200},
	} {
		w := request(h, "POST", tc.path, tc.body)
		if w.Code != tc.status {
			t.Fatalf("%s: got %d want %d: %s", tc.path, w.Code, tc.status, w.Body.String())
		}
	}
}
