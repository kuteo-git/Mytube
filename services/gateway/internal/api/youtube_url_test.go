package api

import "testing"

func TestVideoIDFromSearch(t *testing.T) {
	const id = "BjGHyQhYwHU"

	cases := []struct {
		name      string
		query     string
		wantID    string
		wantIsURL bool
	}{
		// The five shapes settled with the user.
		{"desktop", "https://www.youtube.com/watch?v=" + id, id, true},
		{"no www", "https://youtube.com/watch?v=" + id, id, true},
		{"mobile web", "https://m.youtube.com/watch?v=" + id, id, true},
		{"shortened", "https://youtu.be/" + id, id, true},
		{"app scheme", "vnd.youtube://" + id, id, true},
		{"app scheme, no slashes", "vnd.youtube:" + id, id, true},

		// Shapes that cost nothing to accept, being the same id in the same place.
		{"shorts", "https://www.youtube.com/shorts/" + id, id, true},
		{"live", "https://www.youtube.com/live/" + id, id, true},
		{"embed", "https://www.youtube.com/embed/" + id, id, true},
		{"music", "https://music.youtube.com/watch?v=" + id, id, true},

		// A timestamp is the ordinary way somebody points at a moment, and a
		// video reached through a playlist is still one video.
		{"with a timestamp", "https://youtu.be/" + id + "?t=90", id, true},
		{"in a playlist", "https://www.youtube.com/watch?v=" + id + "&list=PL123", id, true},
		{"desktop with a timestamp", "https://www.youtube.com/watch?v=" + id + "&t=1m30s", id, true},

		// Copied out of an address bar, which drops the scheme.
		{"no scheme", "youtu.be/" + id, id, true},
		{"no scheme, desktop", "www.youtube.com/watch?v=" + id, id, true},

		// Surrounding whitespace comes with almost every paste.
		{"padded", "  https://youtu.be/" + id + "  ", id, true},

		// An ordinary search must not be read as an address. `url.Parse` accepts
		// a bare word with an empty host, so this is the branch that decides
		// whether every search goes down the fetch road by mistake.
		{"a word", "cats", "", false},
		{"a phrase", "how to cook rice", "", false},
		{"a phrase with a dot", "vue.js tutorial", "", false},
		{"somebody else's site", "https://vimeo.com/12345", "", false},
		{"a channel handle as a term", "@tinhte", "", false},

		// An address to YouTube naming no video is still an address. Saying so
		// is what lets the caller decline to run the URL's text as a search.
		{"a channel", "https://www.youtube.com/@tinhte", "", true},
		{"a search page", "https://www.youtube.com/results?search_query=cats", "", true},
		{"the front page", "https://www.youtube.com/", "", true},
		{"a playlist alone", "https://www.youtube.com/playlist?list=PL123", "", true},

		// An id is eleven characters of a known alphabet. Matched exactly, so a
		// path segment that merely sits where an id would sit cannot pass.
		{"too short", "https://youtu.be/abc", "", true},
		{"too long", "https://youtu.be/" + id + "XXXX", "", true},
		{"bad character", "https://youtu.be/BjGHyQhYw!U", "", true},

		{"empty", "", "", false},
		{"whitespace", "   ", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotID, gotIsURL := videoIDFromSearch(tc.query)
			if gotID != tc.wantID || gotIsURL != tc.wantIsURL {
				t.Errorf("videoIDFromSearch(%q) = (%q, %v), want (%q, %v)",
					tc.query, gotID, gotIsURL, tc.wantID, tc.wantIsURL)
			}
		})
	}
}
