package api

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
)

// A pasted channel address, read the same way a pasted video address is.
//
// The sibling of videoIDFromSearch in youtube_url.go, and separate from it for
// one reason: a video address and a channel address name different things, and
// a parser that answered "channel" for a watch link would send somebody to a
// channel page when they asked for a video.
//
// Only two of the four shapes YouTube writes carry anything usable — the id and
// the handle — and they are not worth the same. CLAUDE.md records the
// measurement on @tinhte: handle resolution failed, the listing fell back to a
// flat playlist, and every card on that channel's page rendered with no upload
// date and no view count while other channels showed both. A `UC…` id *is* an
// InnerTube browseId and needs no lookup at all. So the id is taken wherever
// the address carries one, and a handle is only ever the means of finding one.

// A channel id is 24 characters beginning `UC`, in the same alphabet as a video
// id. Matched exactly so that a path segment merely sitting where an id would
// sit cannot be mistaken for one.
var channelIDPattern = regexp.MustCompile(`^UC[A-Za-z0-9_-]{22}$`)

// A handle is what YouTube shows now: `@` and then the name. The characters are
// narrower than a URL path allows, which is what keeps `@` in an email address
// from being read as one.
var handlePattern = regexp.MustCompile(`^@[A-Za-z0-9_.-]{3,30}$`)

// channelRef is what an address named: an id, a handle, or neither.
//
// Both fields rather than one string with a prefix, because the caller does
// different things with them — an id is used as it stands, a handle has to be
// resolved — and a type that made them the same shape would push that
// distinction into a string comparison at every use.
type channelRef struct {
	ID     string
	Handle string
}

func (c channelRef) empty() bool { return c.ID == "" && c.Handle == "" }

// channelFromSearch reads a channel out of a pasted address.
//
// The second return says whether the query was a channel address at all, so an
// ordinary search term can be told from an address that named no channel this
// can open. The first goes to search as it always did; the second is worth
// saying out loud rather than silently searching for the text of a URL.
func channelFromSearch(query string) (channelRef, bool) {
	q := strings.TrimSpace(query)
	if q == "" {
		return channelRef{}, false
	}

	// A handle typed on its own, which is how people say a channel out loud.
	if handlePattern.MatchString(q) {
		return channelRef{Handle: q}, true
	}

	// Everything else has to look like an address before it is read as one.
	withScheme := q
	if !strings.Contains(q, "://") {
		if !strings.HasPrefix(strings.ToLower(q), "www.") &&
			!strings.HasPrefix(strings.ToLower(q), "youtube.com") &&
			!strings.HasPrefix(strings.ToLower(q), "m.youtube.com") {
			return channelRef{}, false
		}
		withScheme = "https://" + q
	}

	parsed, err := url.Parse(withScheme)
	if err != nil {
		return channelRef{}, false
	}
	host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	if host != "youtube.com" && host != "m.youtube.com" && host != "music.youtube.com" {
		return channelRef{}, false
	}

	segments := make([]string, 0, 4)
	for _, s := range strings.Split(parsed.Path, "/") {
		if s != "" {
			segments = append(segments, s)
		}
	}
	if len(segments) == 0 {
		return channelRef{}, false
	}

	switch {
	case strings.HasPrefix(segments[0], "@"):
		if !handlePattern.MatchString(segments[0]) {
			return channelRef{}, false
		}
		return channelRef{Handle: segments[0]}, true

	case segments[0] == "channel" && len(segments) > 1:
		if !channelIDPattern.MatchString(segments[1]) {
			return channelRef{}, false
		}
		return channelRef{ID: segments[1]}, true

	// The forms YouTube has retired but still redirects. Neither carries an id,
	// and both are read as a handle: `/user/PewDiePie` and `/c/PewDiePie` are
	// the same name the handle is built from, and asking upstream is the only
	// way to be sure either way.
	case (segments[0] == "user" || segments[0] == "c") && len(segments) > 1:
		candidate := "@" + segments[1]
		if !handlePattern.MatchString(candidate) {
			return channelRef{}, false
		}
		return channelRef{Handle: candidate}, true
	}

	return channelRef{}, false
}

// handleResolveChannel answers "which channel does this address name", and makes
// sure the library has a row for it before saying so.
//
// The library first, always. 1626 of this library's 1690 channels carry a
// handle, so a pasted address usually needs no upstream request at all — and
// the one it would need is counted against the address §8 risk 6 is about.
//
// Only when the handle is unknown does it ask, and then it writes the answer
// down: the channel page reads the catalog, so a channel with no row there is a
// page that cannot be drawn however well the address was understood. Exactly
// the shape `handleEnsureExternal` already uses for a pasted video link.
func (g *Gateway) handleResolveChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query().Get("q")

	ref, isAddress := channelFromSearch(query)
	if !isAddress || ref.empty() {
		// Not a mistake worth an error: the caller asks about every search, and
		// most searches are not addresses.
		writeJSON(w, http.StatusOK, map[string]any{"channel": nil})
		return
	}

	userID := g.userID(r)

	// Known already? Then nothing upstream happens, and a channel the household
	// follows opens as fast as any page in the app.
	found, err := g.catalog.GetChannel(ctx, connect.NewRequest(&catalogv1.GetChannelRequest{
		ChannelId: ref.ID,
		Handle:    ref.Handle,
		UserId:    userID,
	}))
	if err == nil && found.Msg.GetChannel() != nil {
		writeJSON(w, http.StatusOK, map[string]any{"channel": found.Msg.GetChannel().GetId()})
		return
	}

	// Never seen. One request upstream, for the header rather than the videos.
	key := ref.Handle
	if key == "" {
		key = ref.ID
	}
	resolved, err := g.ingest.ResolveChannel(ctx, connect.NewRequest(&ingestv1.ResolveChannelRequest{
		Channel: key,
	}))
	if err != nil {
		g.logger.Warn("resolve channel", "query", query, "error", err)
		writeJSON(w, http.StatusOK, map[string]any{"channel": nil})
		return
	}

	// Written down before it is handed over, because the page that opens next
	// reads the catalog and not this answer.
	if _, err := g.catalog.UpsertChannel(ctx, connect.NewRequest(&catalogv1.UpsertChannelRequest{
		Channel: &catalogv1.Channel{
			Id:              resolved.Msg.GetChannelId(),
			Name:            resolved.Msg.GetName(),
			Handle:          resolved.Msg.GetHandle(),
			AvatarPath:      resolved.Msg.GetAvatarUrl(),
			SubscriberCount: resolved.Msg.GetSubscriberCount(),
		},
	})); err != nil {
		// The address was understood and the channel exists; only the note
		// failed. Say so and let the page try — it may well find the row was
		// written by something else in the meantime.
		g.logger.Warn("record resolved channel", "channel", resolved.Msg.GetChannelId(), "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{"channel": resolved.Msg.GetChannelId()})
}
