package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func webDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>app</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "main-abc123.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func apiEcho() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("api"))
	})
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// The whole reason the fallback runs api first: an unknown /api/ path must stay
// an API answer, not become index.html under a 200.
func TestWebHandlerLeavesAPIAlone(t *testing.T) {
	h := WebHandler(webDir(t), apiEcho())
	for _, path := range []string{"/api/feed", "/api/nothing-here", "/healthz"} {
		if rec := get(t, h, path); rec.Code != http.StatusTeapot || rec.Body.String() != "api" {
			t.Errorf("%s: got %d %q, want the api handler", path, rec.Code, rec.Body.String())
		}
	}
}

// A reloaded client-side route is the case that makes this a single-page app
// rather than a directory of files.
func TestWebHandlerFallsBackToIndex(t *testing.T) {
	h := WebHandler(webDir(t), apiEcho())
	for _, path := range []string{"/", "/watch", "/settings/feed", "/channel/UC123"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusOK || rec.Body.String() != "<html>app</html>" {
			t.Errorf("%s: got %d %q, want index.html", path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control %q, want no-store", path, got)
		}
	}
}

func TestWebHandlerCachesFingerprintedAssetsOnly(t *testing.T) {
	h := WebHandler(webDir(t), apiEcho())

	rec := get(t, h, "/assets/main-abc123.js")
	if rec.Code != http.StatusOK || rec.Body.String() != "console.log(1)" {
		t.Fatalf("asset: got %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("asset Cache-Control %q, want immutable", got)
	}

	// index.html is what names the current build; caching it asks a returning
	// browser for asset files the next deploy has already replaced.
	if got := get(t, h, "/index.html").Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("index Cache-Control %q, want no-store", got)
	}
}
