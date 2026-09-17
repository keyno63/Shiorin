package httpapi

import (
	"embed"
	"net/http"
)

//go:embed web/index.html web/app.css web/app.js
var webFiles embed.FS

func addUIRoutes(mux *http.ServeMux) {
	for _, asset := range []struct{ route, file, contentType string }{
		{"GET /{$}", "web/index.html", "text/html; charset=utf-8"},
		{"GET /assets/app.css", "web/app.css", "text/css; charset=utf-8"},
		{"GET /assets/app.js", "web/app.js", "text/javascript; charset=utf-8"},
	} {
		body, err := webFiles.ReadFile(asset.file)
		if err != nil {
			panic(err) // Embedded assets must exist at build time.
		}
		mux.HandleFunc(asset.route, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", asset.contentType)
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
			if r.Method != http.MethodHead {
				_, _ = w.Write(body)
			}
		})
	}
}
