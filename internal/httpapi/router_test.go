package httpapi_test

import (
	"encoding/json"
	"github.com/keyno63/Shiorin/internal/auth"
	"github.com/keyno63/Shiorin/internal/bookmark"
	"github.com/keyno63/Shiorin/internal/httpapi"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func request(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func authenticatedHandler(t *testing.T) http.Handler {
	t.Helper()
	h := httpapi.New(&bookmark.Memory{}, auth.New())
	_, token := registerAndLogin(t, h, "tester")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
		h.ServeHTTP(w, r)
	})
}

func TestCreateAndSearch(t *testing.T) {
	h := authenticatedHandler(t)
	for _, body := range []string{
		`{"title":"Go検索入門","url":"https://example.com/1","tags":[" Go ","go","DB"]}`,
		`{"title":"Go応用","url":"https://example.com/2","tags":["go"]}`,
		`{"title":"Scala","url":"https://example.com/3","tags":["db"]}`,
	} {
		w := request(h, "POST", "/bookmarks", body)
		if w.Code != 201 {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
	}
	cases := []struct {
		path         string
		total, count int
		title        string
	}{
		{"/bookmarks?q=GO&tag=db", 1, 1, "Go検索入門"},
		{"/bookmarks?q=go&limit=1&offset=1", 2, 1, "Go検索入門"},
		{"/bookmarks?q=Scala", 1, 1, "Scala"},
		{"/bookmarks?q=missing", 0, 0, ""},
		{"/bookmarks?offset=100", 3, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			w := request(h, "GET", tc.path, "")
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
			if result.Total != tc.total || len(result.Items) != tc.count || result.Items == nil {
				t.Fatalf("unexpected result: %s", w.Body.String())
			}
			if tc.count > 0 && result.Items[0].Title != tc.title {
				t.Fatalf("wrong result: %s", w.Body.String())
			}
		})
	}
}

func TestInvalidRequests(t *testing.T) {
	h := authenticatedHandler(t)
	cases := []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/bookmarks", `{}`, 400},
		{"POST", "/bookmarks", `{"title":"x","url":"file:///tmp/a"}`, 400},
		{"POST", "/bookmarks", `{"title":"x","url":"https://example.com","unknown":1}`, 400},
		{"POST", "/bookmarks", `{"title":"x","url":"https://example.com"} {}`, 400},
		{"POST", "/bookmarks", `{"title":"x","url":"https://example.com","note":"` + strings.Repeat("x", 1<<20) + `"}`, 413},
		{"GET", "/bookmarks?limit=0", "", 400},
		{"GET", "/bookmarks?offset=-1", "", 400},
		{"GET", "/bookmarks?limit=no", "", 400},
		{"DELETE", "/bookmarks", "", 405},
		{"GET", "/missing", "", 404},
		{"GET", "/healthz", "", 200},
	}
	for _, tc := range cases {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := request(h, tc.method, tc.path, tc.body)
			if w.Code != tc.status {
				t.Fatalf("got %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}
