package api

import (
	"net/url"
	"regexp"
	"strings"
)

// A pasted address is not a question.
//
// Search runs `ytsearch20:<the string>` — it asks YouTube to *look for* the
// text. Given an address that is the wrong verb: it spends one counted upstream
// request (CLAUDE.md §8, risk 5) to go hunting for something whose location was
// already stated. And the library half of the page is full-text over titles and
// channels, which an address never matches, so pasting a link to a video
// already on disk answered "Nothing here matches".
//
// So an address is recognised and *fetched* instead of searched. The five
// shapes below all carry the same eleven-character id, which is the only thing
// any of them are worth reading for.

// YouTube ids are eleven characters of an unpadded base64url alphabet. Matched
// exactly rather than loosely, so that a path segment which merely sits where an
// id would sit — `/watch`, `/results`, a locale prefix — cannot be mistaken for
// one.
var videoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// Paths whose first segment is followed by the id: youtu.be is the odd one out
// in carrying the id as the whole path.
var idBearingPrefixes = []string{"shorts", "live", "embed", "v"}

// videoIDFromSearch reads a YouTube video id out of a pasted address.
//
// The second return says whether the query was an address at all. An ordinary
// search term must be told apart from an address that named no video — the
// first goes to search as it always did, the second is a mistake worth
// reporting rather than silently searching for the text of a URL.
func videoIDFromSearch(query string) (id string, isAddress bool) {
	q := strings.TrimSpace(query)
	if q == "" {
		return "", false
	}

	// The app scheme, which is what a phone hands over when the YouTube app is
	// the one doing the sharing. Both spellings are seen in the wild; neither
	// parses as a URL with a host, so it is read before anything else.
	for _, scheme := range []string{"vnd.youtube://", "vnd.youtube:", "youtube://"} {
		if rest, ok := cutPrefixFold(q, scheme); ok {
			return matchID(firstSegment(trimQuery(rest))), true
		}
	}

	// Everything else has to look like a web address. A bare word must not be
	// parsed as a URL: `url.Parse("cats")` succeeds happily with an empty host,
	// and treating that as an address would send every ordinary search down
	// this road.
	if !strings.Contains(q, "/") && !strings.Contains(q, ".") {
		return "", false
	}
	if !strings.HasPrefix(q, "http://") && !strings.HasPrefix(q, "https://") {
		// Addresses are copied without their scheme often enough to be worth
		// accepting — `youtu.be/ID` is what a phone shows in the address bar.
		q = "https://" + q
	}

	parsed, err := url.Parse(q)
	if err != nil {
		return "", false
	}

	host := strings.ToLower(parsed.Hostname())
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.") // the mobile web address
	if host != "youtube.com" && host != "youtu.be" && host != "music.youtube.com" {
		return "", false
	}

	// From here the query *is* an address to YouTube, whatever else is true of
	// it — a channel, a playlist, a search results page. Saying so lets the
	// caller decline to run those as text searches.
	segments := pathSegments(parsed.Path)

	if host == "youtu.be" {
		if len(segments) == 0 {
			return "", true
		}
		return matchID(segments[0]), true
	}

	// The desktop and mobile-web form. `list` is deliberately ignored: a link to
	// a video inside a playlist is still a link to one video, and the person
	// pasting it pointed at that video.
	if v := parsed.Query().Get("v"); v != "" {
		return matchID(v), true
	}

	if len(segments) >= 2 {
		for _, prefix := range idBearingPrefixes {
			if strings.EqualFold(segments[0], prefix) {
				return matchID(segments[1]), true
			}
		}
	}

	return "", true
}

// `t=` is read but not acted on yet. Opening a pasted link at the moment it
// points to is a real thing to want, and every position in this player is
// absolute (§4), so it would fit — but it is a decision about playback, not
// about search, and this change is only about finding the video.

func matchID(candidate string) string {
	if videoIDPattern.MatchString(candidate) {
		return candidate
	}
	return ""
}

func pathSegments(path string) []string {
	out := make([]string, 0, 2)
	for _, segment := range strings.Split(path, "/") {
		if segment != "" {
			out = append(out, segment)
		}
	}
	return out
}

func firstSegment(s string) string {
	if i := strings.IndexAny(s, "/"); i >= 0 {
		return s[:i]
	}
	return s
}

func trimQuery(s string) string {
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		return s[:i]
	}
	return s
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return "", false
}
