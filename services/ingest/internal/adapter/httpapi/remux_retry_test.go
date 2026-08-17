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

// complainingRemuxer produces a perfectly good-looking stream and grumbles on
// the way out — which is what a mux does when one of its two inputs dies part
// way through. The picture keeps arriving; the sound stops.
type complainingRemuxer struct {
	resolves int
	urls     []string
}

func (c *complainingRemuxer) ResolveRemuxURLs(context.Context, string, int32) ([]string, error) {
	c.resolves++
	return []string{"pair"}, nil
}

func (c *complainingRemuxer) OpenRemux(_ context.Context, urls []string, _, _ float64) (io.ReadCloser, error) {
	c.urls = append(c.urls, urls[0])
	return &grumbling{Reader: strings.NewReader("ftypiso5moof")}, nil
}

func (c *complainingRemuxer) ProbeKeyframe(context.Context, string, float64) (float64, error) {
	return 0, nil
}

type grumbling struct{ *strings.Reader }

func (g *grumbling) Close() error   { return nil }
func (g *grumbling) Stderr() string { return "Server returned 403 Forbidden (access denied)" }

// A mux can half work, and half working used to be indistinguishable from
// working. One input is refused while the other carries on, and the viewer gets
// a video whose picture runs for a minute and whose sound stops at 0.81s —
// observed, and reported by the browser only as PIPELINE_ERROR_DECODE.
//
// Bytes flowed, so nothing was logged and nothing was retried; worse, the URL
// pair stayed cached for ninety minutes, so every attempt at that video for the
// next hour and a half rebuilt the same broken stream.
func TestAMuxThatComplainsDropsItsCachedURLs(t *testing.T) {
	remux := &complainingRemuxer{}
	h := NewHandler(remux, fixedSource{url: "https://youtu.be/abc"}, nil, 1080, discardLogger())

	mux := http.NewServeMux()
	h.Routes(mux)
	for range 2 {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream/abc", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
	}

	// Twice, not once: the first request's complaint must have emptied the cache
	// that the second would otherwise have been served from.
	if remux.resolves != 2 {
		t.Fatalf("resolves = %d, want 2 — the complaint did not drop the cached pair", remux.resolves)
	}
}
