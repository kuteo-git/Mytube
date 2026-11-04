package ytdlp

import (
	"testing"

	goytdlp "github.com/lrstanley/go-ytdlp"
)

func ptr[T any](v T) *T { return &v }

// A search for a singer's name answers with the singer's channel among the
// videos — measured on the household's gateway for "son tung": id
// UClyA28-01x4z60eWQ2kiNbA, sourceUrl https://www.youtube.com/channel/…,
// duration 0. Listed as a video, opening it asked ingest to fetch a channel
// as one video.
func TestSearchResultsAreVideosOnly(t *testing.T) {
	infos := []*goytdlp.ExtractedInfo{
		{ID: "FN7ALfpGxiI", Title: ptr("NƠI NÀY CÓ ANH"), WebpageURL: ptr("https://www.youtube.com/watch?v=FN7ALfpGxiI")},
		{ID: "UClyA28-01x4z60eWQ2kiNbA", Title: ptr("Sơn Tùng M-TP Official"), WebpageURL: ptr("https://www.youtube.com/channel/UClyA28-01x4z60eWQ2kiNbA")},
		{ID: "PLx0sYbCqOb8TBPRdmBHs5Iftvv9TPboYG", Title: ptr("A playlist")},
		{ID: ""},
		{ID: "xypzmu5mMPY", Title: ptr("MUỘN RỒI MÀ SAO CÒN")},
	}

	got := searchResults(infos)

	var ids []string
	for _, v := range got {
		ids = append(ids, v.ID)
	}
	if len(ids) != 2 || ids[0] != "FN7ALfpGxiI" || ids[1] != "xypzmu5mMPY" {
		t.Fatalf("ids = %v, want the two videos", ids)
	}
}

// The app asks for a larger page each time the reader reaches the bottom: 20,
// 40, 60. Anything over 50 used to fall back to 20, so the third page came
// back shorter than the second and the list shrank under the reader.
func TestSearchLimitIsCappedNotReset(t *testing.T) {
	for _, tc := range []struct{ asked, want int32 }{{0, 20}, {-1, 20}, {20, 20}, {40, 40}, {60, 50}, {500, 50}} {
		if got := searchLimit(tc.asked); got != tc.want {
			t.Errorf("searchLimit(%d) = %d, want %d", tc.asked, got, tc.want)
		}
	}
}
