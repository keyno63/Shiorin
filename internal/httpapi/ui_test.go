package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/keyno63/Shiorin/internal/auth"
	"github.com/keyno63/Shiorin/internal/bookmark"
	"github.com/keyno63/Shiorin/internal/httpapi"
)

func TestUIRoutesKeepAPIAuthenticationAndNotFound(t *testing.T) {
	h := httpapi.New(&bookmark.Memory{}, auth.New())
	for _, tc := range []struct{ path, contentType string }{
		{"/", "text/html"},
		{"/assets/app.css", "text/css"},
		{"/assets/app.js", "text/javascript"},
	} {
		w := request(h, http.MethodGet, tc.path, "")
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), tc.contentType) || w.Body.Len() == 0 {
			t.Fatalf("asset %s: status=%d headers=%v", tc.path, w.Code, w.Header())
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatalf("missing UI security policy for %s", tc.path)
		}
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/bookmarks", 401},
		{"GET", "/me", 401},
		{"GET", "/missing", 404},
		{"GET", "/assets/missing.js", 404},
		{"GET", "/assets/../web/index.html", 301},
		{"POST", "/", 405},
	} {
		if w := request(h, tc.method, tc.path, ""); w.Code != tc.status {
			t.Errorf("%s %s: got %d, want %d", tc.method, tc.path, w.Code, tc.status)
		}
	}
	if w := request(h, http.MethodHead, "/", ""); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("HEAD /: status=%d body=%s", w.Code, w.Body.String())
	}
}
