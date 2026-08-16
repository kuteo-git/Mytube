package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A live Google session must not cross the LAN in the clear.
//
// §3 leaves media URLs unprotected because the LAN is trusted, and this is the
// one place that reasoning does not carry: "trusted with a film" and "trusted
// with somebody's Google session" are different sentences. Anyone reading this
// body is signed in as that person, everywhere.
func TestPastingASessionOverPlainHTTPIsRefused(t *testing.T) {
	g := &Gateway{devUserID: "u_luc"}
	req := httptest.NewRequest(http.MethodPut, "/api/settings/youtube-account",
		strings.NewReader(`{"cookies":"# Netscape HTTP Cookie File\n"}`))
	req.Host = "mac.local:8180"
	req.TLS = nil

	rec := httptest.NewRecorder()
	g.handleYouTubeAccount(rec, req)

	if rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("status = %d, want 426 for a session sent in the clear", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "Netscape") {
		t.Error("the refusal echoed the paste back")
	}
}

// Behind Caddy's TLS, which is what the household actually browses through, and
// on the machine talking to itself — which has no network to be overheard on,
// and which refusing would make the feature untestable where it runs.
//
// The guard is exercised on its own rather than through the handler: past it
// the request goes to ingest, and what is being asserted here is the gate.
func TestTLSAndLoopbackAreAllowedThrough(t *testing.T) {
	cases := map[string]func(*http.Request){
		"behind Caddy": func(r *http.Request) {
			r.Host = "mac.local"
			r.Header.Set("X-Forwarded-Proto", "https")
		},
		"localhost": func(r *http.Request) { r.Host = "localhost:8180" },
		"127.0.0.1": func(r *http.Request) { r.Host = "127.0.0.1:8180" },
		"ipv6 loopback": func(r *http.Request) { r.Host = "[::1]:8180" },
	}
	for name, setup := range cases {
		req := httptest.NewRequest(http.MethodPut, "/api/settings/youtube-account", nil)
		setup(req)
		rec := httptest.NewRecorder()

		if !requireSecureTransport(rec, req) {
			t.Errorf("%s was refused as insecure", name)
		}
	}
}

// Whose account is being asked about is whoever is asking.
//
// One person's session state is not another's business, and a list of who has
// pasted what is exactly the sort of thing that ends up on a screen somebody
// else is looking at.
func TestTheAccountAskedAboutIsTheViewersOwn(t *testing.T) {
	g := &Gateway{devUserID: "u_luc"}
	req := httptest.NewRequest(http.MethodGet, "/api/settings/youtube-account", nil)
	req.Header.Set("X-User-Id", "u_vo")

	if got := g.userID(req); got != "u_vo" {
		t.Errorf("userID = %q, want the header to win over the default", got)
	}
}

func TestAnUnknownMethodSaysWhatIsAllowed(t *testing.T) {
	g := &Gateway{devUserID: "u_luc"}
	rec := httptest.NewRecorder()
	g.handleYouTubeAccount(rec, httptest.NewRequest(http.MethodPost, "/api/settings/youtube-account", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow == "" {
		t.Error("405 without an Allow header")
	}
}
