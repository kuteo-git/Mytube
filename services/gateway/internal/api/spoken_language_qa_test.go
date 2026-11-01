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
	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
	"github.com/lucnguyen/local-youtube/gen/go/ingest/v1/ingestv1connect"
)

// QA G1: titles stored in decomposed form (NFD), copied byte for byte from
// catalog.videos (_QibWgiYS_Y, 5AsycqoEzDQ; 304 such titles on 2026-11-01).
func TestQADecomposedVietnameseTitleIsVietnamese(t *testing.T) {
	for _, title := range []string{
		"Con Gái Của Mẹ",        // _QibWgiYS_Y
		"THUỶ ĐÀI NẤM SAIGON XƯA", // 5AsycqoEzDQ
	} {
		if !vietnameseTitle(title) {
			t.Errorf("%q (NFD) not read as Vietnamese", title)
		}
	}
}

type slowIngest struct {
	ingestv1connect.IngestServiceClient
	delay time.Duration
	err   error
}

func (s *slowIngest) SpokenLanguage(ctx context.Context, _ *connect.Request[ingestv1.SpokenLanguageRequest]) (*connect.Response[ingestv1.SpokenLanguageResponse], error) {
	if s.err != nil {
		return nil, s.err
	}
	select {
	case <-time.After(s.delay):
		return connect.NewResponse(&ingestv1.SpokenLanguageResponse{Language: "en"}), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func askSpoken(g *Gateway) (string, int) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/videos/v/spoken-language", nil)
	req.SetPathValue("id", "v")
	g.handleSpokenLanguage(rec, req)
	var body struct{ Language string }
	_ = json.NewDecoder(rec.Body).Decode(&body)
	return body.Language, rec.Code
}

// QA G2: ingest failing falls back to the title, 200.
func TestQAIngestErrorFallsBackToTitle(t *testing.T) {
	g := &Gateway{catalog: &oneVideo{video: &catalogv1.Video{Id: "v", Title: "Phở Hà Nội ngon nhất"}},
		ingest: &slowIngest{err: errors.New("unavailable")}, logger: discardLogger()}
	if lang, code := askSpoken(g); lang != "vi" || code != 200 {
		t.Fatalf("%q %d", lang, code)
	}
}

// QA G3: ingest slower than the bound: answered from the title by ~5 s.
func TestQASlowIngestIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	g := &Gateway{catalog: &oneVideo{video: &catalogv1.Video{Id: "v", Title: "Never Gonna Give You Up"}},
		ingest: &slowIngest{delay: time.Minute}, logger: discardLogger()}
	start := time.Now()
	lang, code := askSpoken(g)
	if took := time.Since(start); took > spokenAskTimeout+time.Second || lang != "" || code != 200 {
		t.Fatalf("%q %d after %s", lang, code, took)
	}
}
