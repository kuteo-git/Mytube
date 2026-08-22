package api

import "testing"

// Reading a channel out of a pasted address.
//
// The sibling of videoIDFromSearch, and here for the same reason: an address is
// not a question. Pasting a channel link used to run it through search — one
// counted upstream request (§8 risk 5) to hunt for the text of a URL — and the
// page then said "Channel and playlist links cannot be opened here yet", which
// was true and was the whole of what happened.
//
// YouTube writes a channel four ways, and only two of them carry something
// usable: the id, and the handle. The id is worth far more — CLAUDE.md records
// the measurement on @tinhte, where handle resolution failed, the listing fell
// back to a flat playlist, and every card on that channel's page rendered with
// no date and zero views. So the id is preferred wherever the address has one,
// and a handle is only the means of finding an id.
func TestChannelFromSearch(t *testing.T) {
	cases := []struct {
		name    string
		query   string
		want    channelRef
		address bool
	}{
		{
			name:  "a handle, as YouTube writes them now",
			query: "https://youtube.com/@champsnetwork",
			want:  channelRef{Handle: "@champsnetwork"},
			// The address the whole exercise started from.
			address: true,
		},
		{
			// Shared from the app, which appends its own tracking.
			name:    "a handle with the share parameter attached",
			query:   "https://youtube.com/@champsnetwork?si=GXm_j8ad-WG3cCym",
			want:    channelRef{Handle: "@champsnetwork"},
			address: true,
		},
		{
			name:    "a bare id, which needs no resolving at all",
			query:   "https://www.youtube.com/channel/UC-9-kyTW8ZkZNDHQJ6FgpwQ",
			want:    channelRef{ID: "UC-9-kyTW8ZkZNDHQJ6FgpwQ"},
			address: true,
		},
		{
			name:    "a channel page with a tab on the end",
			query:   "https://www.youtube.com/@mkbhd/videos",
			want:    channelRef{Handle: "@mkbhd"},
			address: true,
		},
		{
			name:    "an id with a tab on the end",
			query:   "https://www.youtube.com/channel/UC-9-kyTW8ZkZNDHQJ6FgpwQ/streams",
			want:    channelRef{ID: "UC-9-kyTW8ZkZNDHQJ6FgpwQ"},
			address: true,
		},
		{
			// The old forms. Neither carries an id or a handle, and both are
			// still addresses — worth saying "that names no channel I can open"
			// rather than searching for the text.
			name:    "the legacy user form",
			query:   "https://www.youtube.com/user/PewDiePie",
			want:    channelRef{Handle: "@PewDiePie"},
			address: true,
		},
		{
			name:    "a handle typed on its own",
			query:   "@champsnetwork",
			want:    channelRef{Handle: "@champsnetwork"},
			address: true,
		},
		{
			// A video address is the other rule's business. Answering it here
			// would send somebody to a channel page when they asked for a video.
			name:    "a video link is not a channel link",
			query:   "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			address: false,
		},
		{
			name:    "a playlist is neither",
			query:   "https://www.youtube.com/playlist?list=PL1234567890",
			address: false,
		},
		{
			name:  "an ordinary search is left alone",
			query: "how to read like a pro",
		},
		{
			name:  "an email address is not a handle",
			query: "someone@example.com",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, isAddress := channelFromSearch(c.query)
			if isAddress != c.address {
				t.Fatalf("isAddress = %v, want %v", isAddress, c.address)
			}
			if got != c.want {
				t.Errorf("channelFromSearch(%q) = %+v, want %+v", c.query, got, c.want)
			}
		})
	}
}
