package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
	"github.com/lucnguyen/local-youtube/gen/go/ingest/v1/ingestv1connect"
)

// The title is read only when YouTube has no automatic captions to say which
// language is spoken.
func TestAVietnameseTitleIsReadAsVietnamese(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"Hướng dẫn nấu phở bò Hà Nội", true},
		{"TRỊNH CÔNG SƠN | Diễm Xưa", true},
		{"How to make Phở at home", false}, // one borrowed word
		{"Ça va? Não, obrigado — café à la française", false},
		{"Rick Astley - Never Gonna Give You Up", false},
		{"Nhac Tre Khong Dau", false}, // written without marks: not known
	}
	for _, c := range cases {
		if got := vietnameseTitle(c.title); got != c.want {
			t.Errorf("%q: %v, want %v", c.title, got, c.want)
		}
	}
}

type spokenIngest struct {
	ingestv1connect.IngestServiceClient
	lang  string
	err   error
	asked int
}

func (s *spokenIngest) SpokenLanguage(context.Context, *connect.Request[ingestv1.SpokenLanguageRequest]) (*connect.Response[ingestv1.SpokenLanguageResponse], error) {
	s.asked++
	if s.err != nil {
		return nil, s.err
	}
	return connect.NewResponse(&ingestv1.SpokenLanguageResponse{Language: s.lang}), nil
}

// Titles stored decomposed (NFD), a base letter and a combining mark, copied
// byte for byte from catalog.videos (_QibWgiYS_Y, 5AsycqoEzDQ; 304 such titles on 2026-11-01).
func TestADecomposedVietnameseTitleIsVietnamese(t *testing.T) {
	for _, title := range []string{
		"Con Gái Của Mẹ",        // _QibWgiYS_Y
		"THUỶ ĐÀI NẤM SAIGON XƯA", // 5AsycqoEzDQ
	} {
		if !vietnameseTitle(title) {
			t.Errorf("%q (NFD) not read as Vietnamese", title)
		}
	}
}

// The catalogue first, YouTube (through ingest) when it does not know, and
// the title only when neither does.
func TestTheSpokenLanguageIsAskedInOrder(t *testing.T) {
	ask := func(v *catalogv1.Video, fromIngest string) (string, int) {
		in := &spokenIngest{lang: fromIngest}
		g := &Gateway{catalog: &oneVideo{video: v}, ingest: in, logger: discardLogger()}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/videos/v/spoken-language", nil)
		req.SetPathValue("id", "v")
		g.handleSpokenLanguage(rec, req)
		var body struct{ Language string }
		_ = json.NewDecoder(rec.Body).Decode(&body)
		return body.Language, in.asked
	}
	if lang, asked := ask(&catalogv1.Video{Id: "v", SpokenLanguage: "en", Title: "Phở Hà Nội ngon nhất"}, "vi"); lang != "en" || asked != 0 {
		t.Errorf("known: %q, asked YouTube %d times", lang, asked)
	}
	if lang, asked := ask(&catalogv1.Video{Id: "v", Title: "Phở Hà Nội ngon nhất"}, "en"); lang != "en" || asked != 1 {
		t.Errorf("YouTube's word over the title: %q (%d)", lang, asked)
	}
	if lang, _ := ask(&catalogv1.Video{Id: "v", Title: "Phở Hà Nội ngon nhất"}, ""); lang != "vi" {
		t.Errorf("the title: %q", lang)
	}
	g := &Gateway{catalog: &oneVideo{}, ingest: &spokenIngest{err: connect.NewError(connect.CodeUnavailable, nil)}, logger: discardLogger()}
	if lang := g.spokenLanguage(context.Background(), &catalogv1.Video{Id: "v", Title: "Phở Hà Nội ngon nhất"}); lang != "vi" {
		t.Errorf("ingest down, the title: %q", lang)
	}
	if lang, _ := ask(&catalogv1.Video{Id: "v", Title: "Never Gonna Give You Up"}, ""); lang != "" {
		t.Errorf("not known: %q", lang)
	}
}
