package api

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// WebFS holds the embedded web UI files. Set this before calling NewServer
// if you want to serve the UI.
var WebFS *embed.FS

// RegisterWebUI registers the embedded web UI handler on the router.
func RegisterWebUI(router *Router, webFS *embed.FS) {
	if webFS == nil {
		return
	}

	subFS, err := fs.Sub(*webFS, "web/dist")
	if err != nil {
		return
	}

	fileServer := http.FileServer(http.FS(subFS))

	// Serve static assets directly
	router.Handle("GET", "/assets/{file}", func(w http.ResponseWriter, r *http.Request) {
		fileServer.ServeHTTP(w, r)
	})

	// Serve index.html for the root and any non-API paths (SPA fallback)
	router.Handle("GET", "/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/internal/") {
			http.NotFound(w, r)
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
