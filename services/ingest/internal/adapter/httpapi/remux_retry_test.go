package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// flakyRemuxer hands out a fresh set of URLs on every resolve, and produces a
// stream only for the second set. The first stands in for a URL YouTube signs
// and then refuses: ffmpeg starts, reads nothing, and exits.
type flakyRemuxer struct {
	resolves int
	opened   []string
}

func (f *flakyRemuxer) ResolveRemuxURLs(context.Context, string, int32) ([]string, error) {
	f.resolves++
	if f.resolves == 1 {
		return []string{"dead"}, nil
	}
	return []string{"live"}, nil
}

func (f *flakyRemuxer) OpenRemux(_ context.Context, urls []string, _, _ float64) (io.ReadCloser, error) {
	f.opened = append(f.opened, urls[0])
	if urls[0] == "dead" {
		return io.NopCloser(strings.NewReader("")), nil
	}
	return io.NopCloser(strings.NewReader("ftypiso5moof")), nil
}

func (f *flakyRemuxer) ProbeKeyframe(context.Context, string, float64) (float64, error) {
	return 0, nil
}

type fixedSource struct{ url string }

func (s fixedSource) SourceURLFor(context.Context, string) (string, error) { return s.url, nil }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ffmpeg is started, not awaited, so a refused URL used to arrive as a 200 with
// an empty body — which the browser reports only as DEMUXER_ERROR_COULD_NOT_OPEN.
// Worse, the dead URLs stayed cached for 90 minutes, so the video was
// unplayable for that long while every other video played.
func TestARemuxThatProducesNoBytesIsResolvedAgainAndRetried(t *testing.T) {
	remux := &flakyRemuxer{}
	h := NewHandler(remux, fixedSource{url: "https://youtu.be/abc"}, nil, 1080, discardLogger())

	mux := http.NewServeMux()
	h.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream/abc", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	// The bytes read to prove the stream opened are the start of the fMP4. Losing
	// them would cost the initialisation segment, which is unplayable to drop.
	if body := rec.Body.String(); body != "ftypiso5moof" {
		t.Fatalf("body = %q, want the whole second stream", body)
	}
	if remux.resolves != 2 {
		t.Fatalf("resolves = %d, want 2", remux.resolves)
	}
	// The retry is worth nothing unless the cached URLs are dropped first.
	want := []string{"dead", "live"}
	if len(remux.opened) != len(want) || remux.opened[0] != want[0] || remux.opened[1] != want[1] {
		t.Fatalf("opened = %v, want %v", remux.opened, want)
	}
}

// deadRemuxer never produces bytes, however often it is asked.
type deadRemuxer struct{ opens int }

func (d *deadRemuxer) ResolveRemuxURLs(context.Context, string, int32) ([]string, error) {
	return []string{"dead"}, nil
}

func (d *deadRemuxer) OpenRemux(context.Context, []string, float64, float64) (io.ReadCloser, error) {
	d.opens++
	return io.NopCloser(strings.NewReader("")), nil
}

func (d *deadRemuxer) ProbeKeyframe(context.Context, string, float64) (float64, error) {
	return 0, nil
}

// Twice is where it stops. A 502 is the honest answer, and it beats a 200
// carrying nothing — which is what the player could make no sense of.
func TestARemuxThatNeverOpensIsReportedAsAFailure(t *testing.T) {
	remux := &deadRemuxer{}
	h := NewHandler(remux, fixedSource{url: "https://youtu.be/abc"}, nil, 1080, discardLogger())

	mux := http.NewServeMux()
	h.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream/abc", nil))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	if remux.opens != 2 {
		t.Fatalf("opens = %d, want 2 — a third would be one too many", remux.opens)
	}
}
