package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// Fonts are embedded in the plugin binary and served from the plugin's own
// endpoint, so the browser never reaches out to a third-party font host.
//
//go:embed static
var staticFiles embed.FS

// serveStatic answers /static/... without requiring a session: these are fonts
// and licence text, and font requests made from a stylesheet should not depend
// on how the browser decides to attach cookies.
func (p *Plugin) serveStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")

	// The emblem and the App Bar icon, which an admin can replace. Answered
	// from the KV store when they have, and from the embedded copy when they
	// have not — so this path always has something to serve.
	if which, ok := strings.CutPrefix(name, "static/brand/"); ok {
		p.serveBrand(w, r, which)

		return
	}

	// embed.FS has no notion of "..", but reject traversal attempts anyway so
	// the intent of this handler is unambiguous.
	if strings.Contains(name, "..") {
		writeError(w, http.StatusBadRequest, "Invalid path.")
		return
	}

	data, err := fs.ReadFile(staticFiles, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}

	switch {
	case strings.HasSuffix(name, ".woff2"):
		w.Header().Set("Content-Type", "font/woff2")
	case strings.HasSuffix(name, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}

	// The bundle is immutable for a given plugin version.
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
