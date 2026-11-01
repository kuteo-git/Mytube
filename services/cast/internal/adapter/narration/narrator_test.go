package narration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
)

func TestPrepareReadsTheGatewaysWord(t *testing.T) {
	cases := map[string]domain.Narrated{
		`{"status":"ready"}`:     domain.NarratedReady,
		`{"status":"preparing"}`: domain.NarratedPreparing,
		`{"status":"none"}`:      domain.NarratedNone,
		`{"status":"failed"}`:    domain.NarratedNone,
		`{"status":"later"}`:     domain.NarratedPreparing,
	}
	for body, want := range cases {
		var path, method string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path, method = r.URL.Path, r.Method
			_, _ = w.Write([]byte(body))
		}))
		got, err := New(srv.URL+"/", srv.Client()).Prepare(context.Background(), "abc")
		srv.Close()
		if err != nil || got != want {
			t.Errorf("%s: got %q %v, want %q", body, got, err, want)
		}
		if method != http.MethodPost || path != "/api/videos/abc/audio/narrated" {
			t.Errorf("asked %s %s", method, path)
		}
	}
}

func TestAGatewayFailureIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	if _, err := New(srv.URL, srv.Client()).Prepare(context.Background(), "abc"); err == nil {
		t.Fatal("a 502 was read as an answer")
	}
}
