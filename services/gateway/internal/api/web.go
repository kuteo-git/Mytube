package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// WebHandler serves the built web bundle from this same origin, falling back to
// api for everything the app does not own.
//
// Why here rather than Caddy, which §3 names: Caddy is not installed on this
// machine and is a TLS story as much as a static-file one. Until it is, running
// the household's library through `vite` — a dev server, rebuilding on every
// file change, with no cache headers worth the name — is the thing actually in
// the way. One process serving one origin removes it, and the day Caddy arrives
// it takes this route over exactly as it takes /media over.
//
// The fallback direction matters: api is asked *first* for the paths it owns,
// because the bundle's SPA fallback answers everything with index.html and
// would otherwise turn a mistyped /api/ route into a 200 carrying HTML — which
// the client reports as a JSON parse failure, naming nothing.
func WebHandler(dir string, api http.Handler) http.Handler {
	files := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			api.ServeHTTP(w, r)
			return
		}

		clean := filepath.Clean("/" + r.URL.Path)
		info, err := os.Stat(filepath.Join(dir, clean))
		if err != nil || info.IsDir() {
			// A client-side route — /watch, /settings/feed — is not a file and
			// never will be. Answering 404 here is how a reloaded watch page
			// becomes a broken app rather than the page it was on.
			w.Header().Set("Cache-Control", "no-store")
			http.ServeFile(w, r, index)
			return
		}

		// Vite fingerprints everything under /assets/, so those URLs are safe to
		// keep for ever and a television fetching them once is the point. The
		// entry point must never be cached: it is what names the current build,
		// and a stale one asks for asset files a deploy has already replaced.
		if strings.HasPrefix(clean, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-store")
		}
		files.ServeHTTP(w, r)
	})
}
