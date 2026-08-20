package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	"github.com/lucnguyen/local-youtube/gen/go/catalog/v1/catalogv1connect"
	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
	"github.com/lucnguyen/local-youtube/gen/go/ingest/v1/ingestv1connect"
)

// absentVideo is a video the catalogue knows and the disk does not have — the
// case the whole tier ladder exists for.
type absentVideo struct {
	catalogv1connect.CatalogServiceClient
}

func (absentVideo) GetVideo(
	context.Context, *connect.Request[catalogv1.GetVideoRequest],
) (*connect.Response[catalogv1.GetVideoResponse], error) {
	return connect.NewResponse(&catalogv1.GetVideoResponse{Video: &catalogv1.Video{
		Id:         "abc",
		SourceUrl:  "https://www.youtube.com/watch?v=abc",
		MediaState: catalogv1.MediaState_MEDIA_STATE_ABSENT,
	}}), nil
}

// resolveCounter fails every resolve and counts how many times it was asked.
//
// Failing rather than succeeding on purpose: a stub that answered would let the
// handler quietly go on offering the instant tier and the test would still see
// no `instant` in the JSON, for the wrong reason.
type resolveCounter struct {
	ingestv1connect.IngestServiceClient
	calls int
}

func (r *resolveCounter) ResolveStream(
	context.Context, *connect.Request[ingestv1.ResolveStreamRequest],
) (*connect.Response[ingestv1.ResolveStreamResponse], error) {
	r.calls++
	return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("no progressive format"))
}

// The opening tier is the muxed stream, and the progressive one is not offered
// at all.
//
// Measured 2026-08-18 over 16 videos of this library: itag 18 — the only
// progressive rendition YouTube still publishes, and what every video used to
// open on — served the head of the file and refused a mid-file range 12 times
// out of 14, never answering 206. The adaptive tracks behind the muxed tier
// answered 206 at head and middle alike, 13 of 14.
//
// Offering it anyway is what put the player on a source that either would not
// open or died a megabyte in, with no way to climb off.
func TestAVideoWithNoLocalCopyOpensOnTheMuxedTier(t *testing.T) {
	ingest := &resolveCounter{}
	g := &Gateway{logger: discardLogger(), catalog: absentVideo{}, ingest: ingest, mediaRoot: t.TempDir()}

	req := httptest.NewRequest(http.MethodGet, "/api/videos/abc/stream?prefetch=1", nil)
	req.SetPathValue("id", "abc")
	rec := httptest.NewRecorder()
	g.handleStream(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body streamDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
	}

	if body.Instant != nil {
		t.Errorf("instant tier offered: %+v — the rendition it names stops serving a megabyte in", body.Instant)
	}
	if body.Remux == nil {
		t.Fatal("no remux tier offered, so nothing can play before the file lands")
	}
	if body.StreamError != "" {
		t.Errorf("streamError = %q, want none: nothing is broken", body.StreamError)
	}

	// Not merely unused — not asked for. Each resolve is a full metadata fetch
	// repeated three times over, against the address §8 risk 6 is about, to
	// build a tier that is no longer offered.
	if ingest.calls != 0 {
		t.Errorf("ResolveStream called %d times, want 0", ingest.calls)
	}
}
