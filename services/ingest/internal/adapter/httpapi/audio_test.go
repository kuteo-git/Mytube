package httpapi

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// rangeRecorder serves `body` as googlevideo does when willing, and keeps every
// range it was asked for. It refuses the first `refuse` requests with 403.
type rangeRecorder struct {
	mu     sync.Mutex
	body   []byte
	asked  []string
	refuse int
}

func (r *rangeRecorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.asked = append(r.asked, req.Header.Get("Range"))
	refuse := r.refuse > 0
	if refuse {
		r.refuse--
	}
	r.mu.Unlock()
	if refuse {
		http.Error(w, "expired", http.StatusForbidden)
		return
	}
	start, end, ok := parseByteRange(req.Header.Get("Range"))
	if !ok {
		// What googlevideo does with an open-ended request, in effect.
		http.Error(w, "refused", http.StatusForbidden)
		return
	}
	if start >= int64(len(r.body)) {
		http.Error(w, "past the end", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	end = min(end, int64(len(r.body))-1)
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(r.body)))
	w.WriteHeader(http.StatusPartialContent)
	_, _ = w.Write(r.body[start : end+1])
}

func audioRig(t *testing.T, size, refuse int) (*Handler, *rangeRecorder, *hlsRemuxer) {
	t.Helper()
	body := make([]byte, size)
	for i := range body {
		body[i] = byte(i % 251)
	}
	rec := &rangeRecorder{body: body, refuse: refuse}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	remux := &hlsRemuxer{tracks: domain.MediaTracks{
		Videos: []domain.MediaTrack{{URL: srv.URL + "/video", Codec: "avc1.4d401f", Height: 720}},
		Audio:  domain.MediaTrack{URL: srv.URL + "/audio", Codec: "mp4a.40.2", Bitrate: 128_000},
	}}
	return NewHandler(remux, fixedSource{url: "https://youtu.be/abc"}, nil, 720, discardLogger()), rec, remux
}

func assertBounded(t *testing.T, asked []string) {
	t.Helper()
	for _, header := range asked {
		start, end, ok := parseByteRange(header)
		if !ok {
			t.Fatalf("an upstream request carried %q, not a bounded range", header)
		}
		if end-start+1 > audioChunkBytes {
			t.Fatalf("an upstream request asked for %d bytes, over the %d the audio track answers", end-start+1, audioChunkBytes)
		}
	}
}

func TestTheWholeAudioTrackIsServedAPieceAtATime(t *testing.T) {
	size := 2*audioChunkBytes + 12345
	h, upstream, _ := audioRig(t, size, 0)
	rec := serve(t, h, "/audio/abc", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), upstream.body) {
		t.Fatalf("body = %d bytes, want the whole %d", rec.Body.Len(), size)
	}
	if got := rec.Header().Get("Content-Type"); got != "audio/mp4" {
		t.Errorf("content type = %q", got)
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(size) {
		t.Errorf("content length = %q", got)
	}
	if len(upstream.asked) != 3 {
		t.Errorf("upstream requests = %d, want 3", len(upstream.asked))
	}
	assertBounded(t, upstream.asked)
}

func TestARangeIsAnsweredWithJustThoseBytes(t *testing.T) {
	h, upstream, _ := audioRig(t, 5000, 0)
	rec := serve(t, h, "/audio/abc", "bytes=100-199")
	if rec.Code != http.StatusPartialContent || rec.Body.Len() != 100 {
		t.Fatalf("status = %d, %d bytes", rec.Code, rec.Body.Len())
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 100-199/5000" {
		t.Errorf("content range = %q", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), upstream.body[100:200]) {
		t.Error("wrong bytes")
	}
	assertBounded(t, upstream.asked)
}

func TestAnOpenEndedRangeRunsToTheEndInBoundedPieces(t *testing.T) {
	size := audioChunkBytes + 500
	h, upstream, _ := audioRig(t, size, 0)
	rec := serve(t, h, "/audio/abc", "bytes=200-")
	if rec.Code != http.StatusPartialContent || rec.Body.Len() != size-200 {
		t.Fatalf("status = %d, %d bytes", rec.Code, rec.Body.Len())
	}
	if got := rec.Header().Get("Content-Range"); got != fmt.Sprintf("bytes 200-%d/%d", size-1, size) {
		t.Errorf("content range = %q", got)
	}
	assertBounded(t, upstream.asked)
}

func TestARefusedAudioTrackResolvesAgainOnce(t *testing.T) {
	h, upstream, remux := audioRig(t, 3000, 1)
	rec := serve(t, h, "/audio/abc", "")
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), upstream.body) {
		t.Fatalf("status = %d, %d bytes", rec.Code, rec.Body.Len())
	}
	if remux.resolves != 2 {
		t.Errorf("resolves = %d, want 2", remux.resolves)
	}
}

func TestARangePastTheEndIsNotSatisfiable(t *testing.T) {
	h, _, _ := audioRig(t, 1000, 0)
	if rec := serve(t, h, "/audio/abc", "bytes=5000-"); rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d", rec.Code)
	}
}
