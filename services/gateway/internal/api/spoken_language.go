package api

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
)

// The language spoken in a video, for deciding whether to narrate it: a
// Vietnamese video narrated in Vietnamese reads its captions over its own
// voice.
//
//	GET /api/videos/{id}/spoken-language   {"language": "vi" | "en" | … | ""}
//
// The catalogue's answer when it has one — recorded whenever captions are
// fetched, from the listing that fetch makes anyway. Otherwise ingest asks
// YouTube once (and records it), and with no answer from there the title is
// read: Vietnamese letters in it say Vietnamese. Empty is "not known", which
// the player takes as its switch says.

// spokenAskTimeout bounds the one listing ingest makes; the player waits on
// this answer only to decide narration, never to play.
const spokenAskTimeout = 5 * time.Second

func (g *Gateway) handleSpokenLanguage(w http.ResponseWriter, r *http.Request) {
	resp, err := g.catalog.GetVideo(r.Context(), connect.NewRequest(&catalogv1.GetVideoRequest{VideoId: r.PathValue("id")}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"language": g.spokenLanguage(r.Context(), resp.Msg.GetVideo())})
}

// spokenLanguage is the language spoken in v, or empty when it is not known.
func (g *Gateway) spokenLanguage(ctx context.Context, v *catalogv1.Video) string {
	if lang := v.GetSpokenLanguage(); lang != "" {
		return lang
	}
	ctx, cancel := context.WithTimeout(ctx, spokenAskTimeout)
	defer cancel()
	if resp, err := g.ingest.SpokenLanguage(ctx, connect.NewRequest(&ingestv1.SpokenLanguageRequest{VideoId: v.GetId()})); err == nil {
		if lang := resp.Msg.GetLanguage(); lang != "" {
			return lang
		}
	}
	if vietnameseTitle(v.GetTitle()) {
		return "vi"
	}
	return ""
}

// vietnameseTitle reports whether a title is written in Vietnamese: two or
// more letters no other language in the library uses — ă, ơ, ư, đ, and the
// vowels under a dot or a hook. Two, so a word borrowed into an English title
// ("Phở") does not make it Vietnamese; the accents French and Portuguese
// share (á, à, ã, â, ê, ô) are not counted at all.
func vietnameseTitle(title string) bool {
	n := 0
	for _, r := range title {
		if vietnameseOnly[r] {
			if n++; n >= 2 {
				return true
			}
		}
	}
	return false
}

var vietnameseOnly = func() map[rune]bool {
	m := map[rune]bool{}
	for _, r := range "ăằắẳẵặơờớởỡợưừứửữựđ" +
		"ạảậẩẫấầẹẻẽệểễếềỉịọỏộổỗốồụủỳỵỷỹ" +
		"ĂẰẮẲẴẶƠỜỚỞỠỢƯỪỨỬỮỰĐẠẢẬẨẪẤẦẸẺẼỆỂỄẾỀỈỊỌỎỘỔỖỐỒỤỦỲỴỶỸ" {
		m[r] = true
	}
	return m
}()
