package timedtext

import "testing"

// One track is fetched, not all of them: every extra language is another hit on
// the endpoint that is refusing this address, and the player shows one caption
// track at a time.
func TestPickPrefersVietnameseThenEnglish(t *testing.T) {
	cases := []struct {
		name   string
		have   []string
		want   string
		wantOK bool
	}{
		{"Vietnamese wins when it is there", []string{"en", "fr", "vi"}, "vi", true},
		{"English when there is no Vietnamese", []string{"fr", "en", "de"}, "en", true},
		// "en-US" and "en" are one language, the same rule the feed's language
		// filter uses: the primary subtag decides.
		{"a regional tag is the same language", []string{"en-US"}, "en-US", true},
		{"Vietnamese still wins over regional English", []string{"en-GB", "vi-VN"}, "vi-VN", true},
		// Nothing this household reads. Fetching one anyway would spend the
		// request that is scarce on a file nobody can use.
		{"nothing we read", []string{"ja", "ko"}, "", false},
		{"no captions at all", nil, "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tracks := make([]captionTrack, 0, len(c.have))
			for _, lang := range c.have {
				tracks = append(tracks, captionTrack{Lang: lang})
			}
			got, ok := pick(tracks)
			if ok != c.wantOK {
				t.Fatalf("picked=%v, want %v", ok, c.wantOK)
			}
			if ok && got.Lang != c.want {
				t.Errorf("picked %q, want %q", got.Lang, c.want)
			}
		})
	}
}

func TestPrimarySubtag(t *testing.T) {
	for in, want := range map[string]string{
		"en": "en", "en-US": "en", "zh-Hans": "zh", "pt_BR": "pt", "": "",
	} {
		if got := primary(in); got != want {
			t.Errorf("primary(%q) = %q, want %q", in, got, want)
		}
	}
}

// The web app matches subtitle languages exactly — the regex that decides
// whether a track becomes a row in the menu lists `en` and `vi`, not `en-US` —
// so a regional tag has to be normalised before the file is named. yt-dlp did
// this silently on the way past and nothing here had to know.
func TestTheWrittenLanguageIsThePrimarySubtag(t *testing.T) {
	for _, lang := range []string{"en-US", "en-GB", "vi-VN"} {
		got := primary(lang)
		if got != "en" && got != "vi" {
			t.Errorf("primary(%q) = %q, want a bare en or vi", lang, got)
		}
	}
}
