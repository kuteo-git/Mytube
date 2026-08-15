package youtube

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func checkerFor(t *testing.T, handler http.HandlerFunc) *ShortChecker {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c := NewShortChecker(5 * time.Second)
	c.baseURL = server.URL + "/shorts/"
	return c
}

func TestServedAtShortsIsAShort(t *testing.T) {
	c := checkerFor(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	got, err := c.IsShort(context.Background(), "abc")
	if err != nil {
		t.Fatalf("IsShort: %v", err)
	}
	if !got {
		t.Error("a 200 at /shorts/ is the definition of a Short")
	}
}

func TestRedirectToWatchIsNotAShort(t *testing.T) {
	c := checkerFor(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/watch?v=abc", http.StatusSeeOther)
	})
	got, err := c.IsShort(context.Background(), "abc")
	if err != nil {
		t.Fatalf("IsShort: %v", err)
	}
	if got {
		t.Error("a redirect to /watch is YouTube saying this is an ordinary video")
	}
}

// The trap this whole adapter is arranged around.
//
// Go's client follows redirects by default, so left alone it would chase
// /shorts/<id> to /watch and report the 200 that lands there — the same status
// a real Short answers with. Every video in the library would be classified as
// a Short, and the feed would empty itself.
func TestTheRedirectIsNotFollowed(t *testing.T) {
	var hits []string
	c := checkerFor(t, func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		if r.URL.Path == "/watch" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/watch", http.StatusSeeOther)
	})

	got, err := c.IsShort(context.Background(), "abc")
	if err != nil {
		t.Fatalf("IsShort: %v", err)
	}
	if got {
		t.Error("followed the redirect and mistook the destination for the answer")
	}
	if len(hits) != 1 {
		t.Errorf("made %d requests (%v), want exactly one — the redirect is the answer", len(hits), hits)
	}
}

// A 404 or a 429 is not a "no".
//
// Recording either as "not a Short" would close the question permanently on an
// answer YouTube never gave: the column is a tri-state so that an unanswerable
// probe leaves the video unasked for next time.
func TestOtherStatusesAreErrorsRatherThanNo(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError} {
		c := checkerFor(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		})
		if _, err := c.IsShort(context.Background(), "abc"); err == nil {
			t.Errorf("status %d was reported as a definite answer", status)
		}
	}
}
