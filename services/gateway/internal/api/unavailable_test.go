package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// A members-only video came back as a 500 with yt-dlp's stderr in it. 500 says
// "the system broke, try again", and every layer above obliged: the browser
// retried, the retry queued another job, and one video collected thirteen of
// them in two minutes. Nothing was broken — YouTube had given a clear answer.
func TestAnUpstreamRefusalIsAConflictNotAServerError(t *testing.T) {
	g := &Gateway{logger: discardLogger()}
	rec := httptest.NewRecorder()
	err := connect.NewError(connect.CodeAborted,
		errors.New("members_only: ERROR: [youtube] abc: Join this channel to get access to members-only content"))

	g.writeErr(rec, httptest.NewRequest(http.MethodPost, "/api/videos/abc/comments/fetch", nil), err)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != "video_unavailable" {
		t.Fatalf("code = %q", body["code"])
	}
	// The reason is what the browser branches on: it decides whether to offer
	// joining the channel or to say the video is gone.
	if body["reason"] != "members_only" {
		t.Fatalf("reason = %q, want members_only", body["reason"])
	}
}

// Everything else keeps the status it had. A rate limit is a 500 here on
// purpose — it is worth retrying, and a 409 would tell the client to stop.
func TestOtherFailuresKeepTheirStatus(t *testing.T) {
	g := &Gateway{logger: discardLogger()}
	rec := httptest.NewRecorder()

	g.writeErr(rec, httptest.NewRequest(http.MethodGet, "/api/videos/abc/stream", nil),
		errors.New("HTTP Error 429: Too Many Requests"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if _, present := body["code"]; present {
		t.Fatal("a retryable failure was given an unavailability code")
	}
}

func TestUnavailableReasonReadsTheClosedSet(t *testing.T) {
	cases := map[string]string{
		"members_only: ERROR: join this channel": "members_only",
		"private: ERROR: private video":          "private",
		"removed: ERROR: video removed":          "removed",
		// Nothing recognisable still has to answer with something the client
		// can branch on, rather than an empty string.
		"aborted: something else entirely": "unavailable",
	}
	for message, want := range cases {
		if got := unavailableReason(message); got != want {
			t.Fatalf("unavailableReason(%q) = %q, want %q", message, got, want)
		}
	}
}
