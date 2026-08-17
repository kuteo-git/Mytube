package ytdlp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The question is "will this URL serve a byte", and 206 is the only answer that
// means yes. Measured: a refused URL answers 403, or 302 to a host that then
// answers 403 — and following that redirect only spends a second request to
// arrive at the same refusal.
func TestVerifyURLAcceptsOnlyAServedByte(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		wantOK bool
	}{
		{"a byte served", http.StatusPartialContent, true},
		{"refused", http.StatusForbidden, false},
		{"the whole file instead of the range", http.StatusOK, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Range"); got != probeRangeHeader {
					t.Errorf("probe asked for %q, want %q", got, probeRangeHeader)
				}
				if tc.status == http.StatusFound {
					w.Header().Set("Location", "https://elsewhere.example/denied")
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()

			err := verifyURL(context.Background(), server.URL)
			if tc.wantOK && err != nil {
				t.Fatalf("verifyURL: %v, want nil", err)
			}
			if !tc.wantOK && err == nil {
				t.Fatal("verifyURL returned nil for a URL that served nothing")
			}
		})
	}
}

// A redirect is how googlevideo hands a reader to the CDN host that actually
// has the bytes, and every real reader follows it — ffmpeg, the browser, the
// gateway's proxy. Refusing to follow made the probe reject working URLs and
// answer the player with a 502 for a video that was perfectly fetchable.
func TestVerifyURLFollowsARedirectToTheHostThatServes(t *testing.T) {
	serving := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
	}))
	defer serving.Close()

	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, serving.URL, http.StatusFound)
	}))
	defer redirecting.Close()

	if err := verifyURL(context.Background(), redirecting.URL); err != nil {
		t.Fatalf("verifyURL: %v, want nil — the redirect led to a host that serves", err)
	}
}

// And the pairing that made redirects look guilty in the first place: a refused
// URL redirects to a host that answers 403. Followed, that is still a refusal.
func TestVerifyURLRejectsARedirectToAHostThatRefuses(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer refusing.Close()

	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, refusing.URL, http.StatusFound)
	}))
	defer redirecting.Close()

	if err := verifyURL(context.Background(), redirecting.URL); err == nil {
		t.Fatal("a redirect ending in 403 was accepted")
	}
}
