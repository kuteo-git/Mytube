package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"connectrpc.com/connect"

	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
	"github.com/lucnguyen/local-youtube/gen/go/ingest/v1/ingestv1connect"
)

// stubIngest answers ResolveStream from a list, one URL per call, and records
// whether each call asked for a fresh resolve. Every other method is left to
// the embedded nil interface: reaching one is a test bug, and a panic says so
// more clearly than a zero value would.
type stubIngest struct {
	ingestv1connect.IngestServiceClient
	urls     []string
	refreshs []bool
}

func (s *stubIngest) ResolveStream(_ context.Context, req *connect.Request[ingestv1.ResolveStreamRequest]) (*connect.Response[ingestv1.ResolveStreamResponse], error) {
	s.refreshs = append(s.refreshs, req.Msg.GetRefresh())
	url := s.urls[len(s.urls)-1]
	if len(s.refreshs) <= len(s.urls) {
		url = s.urls[len(s.refreshs)-1]
	}
	return connect.NewResponse(&ingestv1.ResolveStreamResponse{Url: url, Height: 360, MimeType: "video/mp4"}), nil
}

// A signed URL is sometimes handed over already dead: it answers 403 for every
// request until it expires, while resolving again yields a working one. Passing
// that 403 through reaches the player as a bare "format error", and because
// ingest caches the resolved URL for the best part of an hour, the video stays
// unplayable for that long — while every other video plays normally.
func TestARefusedInstantURLIsResolvedAgainAndRetried(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		if r.URL.Path == "/dead" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("moov"))
	}))
	defer upstream.Close()

	ingest := &stubIngest{urls: []string{upstream.URL + "/dead", upstream.URL + "/live"}}
	g := &Gateway{logger: discardLogger(), ingest: ingest, streamClient: &http.Client{}}

	req := httptest.NewRequest(http.MethodGet, "/api/videos/abc/instant", nil)
	req.SetPathValue("id", "abc")
	rec := httptest.NewRecorder()
	g.handleInstantStream(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); body != "moov" {
		t.Fatalf("body = %q, want the second URL's content", body)
	}
	if upstreamHits != 2 {
		t.Fatalf("upstream hits = %d, want 2", upstreamHits)
	}
	// The retry is worth nothing unless the cached URL is dropped first: asking
	// again for the same dead URL fails identically.
	want := []bool{false, true}
	if len(ingest.refreshs) != len(want) {
		t.Fatalf("resolve calls = %v, want %v", ingest.refreshs, want)
	}
	for i, refresh := range want {
		if ingest.refreshs[i] != refresh {
			t.Fatalf("resolve calls = %v, want %v", ingest.refreshs, want)
		}
	}
}

// Twice in a row is not a poisoned URL any more. The refusal is the real
// answer, so it goes to the browser rather than costing a third request to an
// address upstream is already counting against.
func TestAnInstantURLRefusedTwiceIsPassedThrough(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer upstream.Close()

	ingest := &stubIngest{urls: []string{upstream.URL + "/a", upstream.URL + "/b"}}
	g := &Gateway{logger: discardLogger(), ingest: ingest, streamClient: &http.Client{}}

	req := httptest.NewRequest(http.MethodGet, "/api/videos/abc/instant", nil)
	req.SetPathValue("id", "abc")
	rec := httptest.NewRecorder()
	g.handleInstantStream(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if upstreamHits != 2 {
		t.Fatalf("upstream hits = %d, want 2 — a third would be one too many", upstreamHits)
	}
}

// googlevideo answers an open-ended request with a redirect to a host that then
// refuses — 403, up to 9 of 12 attempts on one video — while the identical URL
// asked for a bounded range answers 206 every time. "bytes=0-" is exactly what
// Chrome sends to open a video, so this is what kept videos with no local copy
// from starting at all.
func TestAnOpenEndedRangeIsBoundedBeforeItReachesUpstream(t *testing.T) {
	for _, c := range []struct {
		name string
		from string
		want string
	}{
		{"no range at all", "", "bytes=0-2097151"},
		{"chrome opening a video", "bytes=0-", "bytes=0-2097151"},
		{"seeking", "bytes=1000000-", "bytes=1000000-3097151"},
		{"already bounded", "bytes=100-200", "bytes=100-200"},
		{"a suffix range is bounded already", "bytes=-500", "bytes=-500"},
		{"only the first of several", "bytes=0-, 900-", "bytes=0-2097151"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := boundedRange(c.from); got != c.want {
				t.Fatalf("boundedRange(%q) = %q, want %q", c.from, got, c.want)
			}
		})
	}
}

// A client that sends no range is owed the whole file under a 200. Handing it
// the single bounded piece fetched upstream would give it eight megabytes of a
// video and call that the end — and a television's player is the one that may
// not send a range.
func TestARequestWithNoRangeGetsTheWholeFile(t *testing.T) {
	file := bytes.Repeat([]byte("x"), 20<<20)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var first, last int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &first, &last); err != nil {
			if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-", &first); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			last = int64(len(file)) - 1
		}
		if last > int64(len(file))-1 {
			last = int64(len(file)) - 1
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", first, last, len(file)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(file[first : last+1])
	}))
	defer upstream.Close()

	ingest := &stubIngest{urls: []string{upstream.URL}}
	g := &Gateway{logger: discardLogger(), ingest: ingest, streamClient: &http.Client{}}

	req := httptest.NewRequest(http.MethodGet, "/api/videos/abc/instant", nil)
	req.SetPathValue("id", "abc")
	rec := httptest.NewRecorder()
	g.handleInstantStream(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d — a rangeless request is not partial content", rec.Code, http.StatusOK)
	}
	if rec.Body.Len() != len(file) {
		t.Fatalf("body = %d bytes, want %d", rec.Body.Len(), len(file))
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(file)) {
		t.Fatalf("Content-Length = %q, want %d", got, len(file))
	}
}
