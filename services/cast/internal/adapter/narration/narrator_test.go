package narration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
)

func TestPrepareReadsTheGatewaysWord(t *testing.T) {
	cases := map[string]domain.NarratedAnswer{
		`{"status":"ready","stream":"ready"}`:         {File: domain.NarratedReady, Stream: domain.NarratedReady},
		`{"status":"preparing","stream":"ready"}`:     {File: domain.NarratedPreparing, Stream: domain.NarratedReady},
		`{"status":"preparing","stream":"preparing"}`: {File: domain.NarratedPreparing, Stream: domain.NarratedPreparing},
		`{"status":"none","stream":"none"}`:           {File: domain.NarratedNone, Stream: domain.NarratedNone},
		`{"status":"failed","stream":"ready"}`:        {File: domain.NarratedNone, Stream: domain.NarratedReady},
		`{"status":"later"}`:                          {File: domain.NarratedPreparing, Stream: domain.NarratedPreparing},
	}
	for body, want := range cases {
		var path, method, from string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path, method, from = r.URL.Path, r.Method, r.URL.Query().Get("from")
			_, _ = w.Write([]byte(body))
		}))
		got, err := New(srv.URL+"/", srv.Client()).Prepare(context.Background(), "abc", 47)
		srv.Close()
		if err != nil || got != want {
			t.Errorf("%s: got %+v %v, want %+v", body, got, err, want)
		}
		if method != http.MethodPost || path != "/api/videos/abc/audio/narrated" || from != "47" {
			t.Errorf("asked %s %s from=%s", method, path, from)
		}
	}
}

func TestAGatewayFailureIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	if _, err := New(srv.URL, srv.Client()).Prepare(context.Background(), "abc", 0); err == nil {
		t.Fatal("a 502 was read as an answer")
	}
}

func TestSpokenLanguageReadsTheGatewaysWord(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"language":"vi"}`))
	}))
	defer srv.Close()
	got, err := New(srv.URL, srv.Client()).SpokenLanguage(context.Background(), "abc")
	if err != nil || got != "vi" || path != "/api/videos/abc/spoken-language" {
		t.Fatalf("got %q %v from %s", got, err, path)
	}
}
