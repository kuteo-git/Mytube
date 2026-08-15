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
