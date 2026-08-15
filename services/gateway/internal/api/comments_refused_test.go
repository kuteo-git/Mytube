package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	"github.com/lucnguyen/local-youtube/gen/go/catalog/v1/catalogv1connect"
	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
	"github.com/lucnguyen/local-youtube/gen/go/ingest/v1/ingestv1connect"
)

// refusingIngest stands in for the day YouTube says no to the comments API.
type refusingIngest struct {
	ingestv1connect.IngestServiceClient
}

func (refusingIngest) FetchComments(
	context.Context, *connect.Request[ingestv1.FetchCommentsRequest],
) (*connect.Response[ingestv1.FetchCommentsResponse], error) {
	return nil, connect.NewError(connect.CodeInternal, errors.New(
		`fetch comments "https://www.youtube.com/watch?v=abc": exit status 1

ERROR: Unable to download API page: HTTP Error 403: Forbidden`))
}

// emptyCatalog has no comments stored, so the handler goes upstream.
type emptyCatalog struct {
	catalogv1connect.CatalogServiceClient
}

func (emptyCatalog) ListComments(
	context.Context, *connect.Request[catalogv1.ListCommentsRequest],
) (*connect.Response[catalogv1.ListCommentsResponse], error) {
	return connect.NewResponse(&catalogv1.ListCommentsResponse{TotalCount: 0}), nil
}

// A video whose comments YouTube declines is not a broken server.
//
// This used to answer 500, and a 500 is a claim that this system has failed.
// What had actually happened was a temporary "HTTP Error 403" to a request
// nothing on the page depends on — the video played, the description was there
// — and the browser console went red anyway.
//
// Not the 409 a dead *video* gets either (CLAUDE.md §4): that one means
// permanent, offers no retry and names a reason, and this is the opposite.
func TestCommentsRefusedUpstreamIsNotAServerError(t *testing.T) {
	g := &Gateway{logger: discardLogger(), ingest: refusingIngest{}, catalog: emptyCatalog{}}

	req := httptest.NewRequest(http.MethodPost, "/api/videos/abc/comments/fetch", nil)
	req.SetPathValue("id", "abc")
	rec := httptest.NewRecorder()
	g.handleFetchComments(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body fetchCommentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
	}
	// Said outright, so the page can offer a retry rather than showing an empty
	// comment section that looks like a video nobody has spoken about.
	if !body.Unavailable {
		t.Errorf("unavailable = false, want true (body %q)", rec.Body.String())
	}
	if body.Imported != 0 {
		t.Errorf("imported = %d, want 0", body.Imported)
	}
}

// The player asks for the stream answer every five seconds while there is no
// local copy, and every ask used to schedule a download.
//
// While things worked this was invisible: Enqueue is idempotent for as long as
// a job is QUEUED or RUNNING. The moment one failed there was nothing left to
// attach to, and the next poll started another — three jobs in twenty-six
// seconds, measured on 53KMZ_uRJOc against a 403 that had nothing to do with
// how many times it was asked.
func TestTheSameDownloadIsNotScheduledOnEveryPoll(t *testing.T) {
	var g Gateway
	now := time.Now()

	if !g.downloadsAsked.claim("https://youtu.be/abc", now) {
		t.Fatal("the first ask was refused")
	}
	for _, after := range []time.Duration{time.Second, 5 * time.Second, 59 * time.Second} {
		if g.downloadsAsked.claim("https://youtu.be/abc", now.Add(after)) {
			t.Errorf("asked again after %s", after)
		}
	}
	// A different video is a different question.
	if !g.downloadsAsked.claim("https://youtu.be/other", now.Add(time.Second)) {
		t.Error("another video was refused")
	}
	// And the wait does end: nothing here may make a video permanently
	// unfetchable, which is what a cooldown with no exit would be.
	if !g.downloadsAsked.claim("https://youtu.be/abc", now.Add(submitCooldown+time.Second)) {
		t.Error("still refused after the cooldown")
	}
}
