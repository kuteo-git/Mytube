package ytdlp

import (
	"context"
	"fmt"
	"strings"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// Reading one household member's own feeds, as them.
//
// The feed names are domain.Feed*, which are yt-dlp's own aliases. This file
// exists apart from the rest of the downloader for one reason: these are the
// only requests in the system made as somebody.

// The most entries one feed read will take, and what an out-of-range ask means.
//
// Both halves were wrong and the second hid the first. The ceiling was 200,
// below what a playlist needs — this household's "Luke Music" holds 139 and
// the caller asks for 500 — and anything above the ceiling fell back to **50**
// rather than to the ceiling, so asking for more silently returned less than
// asking for 200 would. Raising the caller's limit changed nothing and left no
// trace; the playlists came back at exactly 50 for a second time.
//
// Clamping to the ceiling is the only honest reading of "more than I allow":
// give what is allowed, never quietly give the least.
//
// Two thousand because the household's real playlists are 776 and 1186 videos
// and 500 cut both of them, and because the cost is a page fetch per hundred
// rather than a request per video: measured, 1186 entries in 8s, one
// invocation. Bounded all the same — §8 says a pass always is — and beyond
// 1186 it is unmeasured.
const maxAccountFeedItems = 2000

func clampFeedLimit(limit int32) int32 {
	if limit <= 0 {
		return 50
	}
	if limit > maxAccountFeedItems {
		return maxAccountFeedItems
	}
	return limit
}

// ListAccountFeed reads one feed as the account whose cookies these are.
//
// Flat, like every other listing here: metadata only, no media, and nothing
// downloaded until somebody presses play. What differs from ListPlaylist is the
// session — and the deliberate absence of paging. A subscription feed is read
// from the top for what is new; walking it to the end every hour would be the
// high-volume traffic the account rule exists to keep credentials away from.
func (d *Downloader) ListAccountFeed(
	ctx context.Context, cookiesFile, feed string, limit int32,
) ([]domain.ExternalVideo, error) {
	if cookiesFile == "" {
		return nil, domain.ErrNoAccount
	}
	limit = clampFeedLimit(limit)

	result, err := newAccountCommand(cookiesFile).
		FlatPlaylist().
		DumpJSON().
		PlaylistItems(fmt.Sprintf("1:%d", limit)).
		NoWarnings().
		Run(ctx, feed)
	if err != nil {
		if isAuthFailure(err) {
			return nil, fmt.Errorf("%s: %w", feed, domain.ErrAccountAuth)
		}
		return nil, fmt.Errorf("account feed %q: %w", feed, err)
	}

	infos, err := result.GetExtractedInfo()
	if err != nil {
		return nil, err
	}

	var videos []domain.ExternalVideo
	for _, info := range infos {
		if len(info.Entries) > 0 {
			for _, entry := range info.Entries {
				videos = append(videos, toExternal(entry))
			}
			continue
		}
		if info.ID != "" {
			videos = append(videos, toExternal(info))
		}
	}
	return videos, nil
}

// ListAccountChannels reads the member's whole subscription list.
//
// Uncapped, unlike the video feeds: this is one request whatever the length, and
// a page of it would not be "the newest few" but an arbitrary subset of who
// somebody follows — which is the defect this replaces.
//
// The entries are channels rather than videos, so `id` is the channel id and
// there is no `channel_id` of its own on some of them; both are read, with the
// entry's own id as the fallback.
func (d *Downloader) ListAccountChannels(
	ctx context.Context, cookiesFile string,
) ([]domain.AccountChannel, error) {
	if cookiesFile == "" {
		return nil, domain.ErrNoAccount
	}

	result, err := newAccountCommand(cookiesFile).
		FlatPlaylist().
		DumpJSON().
		NoWarnings().
		Run(ctx, domain.FeedChannels)
	if err != nil {
		// "Failed to resolve url" counts as a dead session *here*, and only
		// here: the URL is a constant in this package, so if it does not resolve
		// the session is the only thing that can have changed. That is how a
		// signed-out request to /feed/channels comes back — measured against a
		// real expired cookies.txt — and reading it as an ordinary failure would
		// leave the account being retried hourly with a cookie that cannot work.
		if isAuthFailure(err) || strings.Contains(err.Error(), "Failed to resolve url") {
			return nil, fmt.Errorf("subscription list: %w", domain.ErrAccountAuth)
		}
		return nil, fmt.Errorf("subscription list: %w", err)
	}

	infos, err := result.GetExtractedInfo()
	if err != nil {
		return nil, err
	}

	var out []domain.AccountChannel
	add := func(id, name string) {
		if strings.HasPrefix(id, "UC") {
			out = append(out, domain.AccountChannel{ID: id, Name: name})
		}
	}
	for _, info := range infos {
		if len(info.Entries) > 0 {
			for _, entry := range info.Entries {
				add(channelIDOf(entry.ChannelID, entry.ID), derefString(entry.Channel))
			}
			continue
		}
		add(channelIDOf(info.ChannelID, info.ID), derefString(info.Channel))
	}
	return out, nil
}

// The channel list names each channel by its own id; channel_id is set too, but
// only the id is guaranteed on a flat listing.
func channelIDOf(channelID *string, id string) string {
	if channelID != nil && *channelID != "" {
		return *channelID
	}
	return id
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ListAccountPlaylists reads the member's playlist list.
//
// The lists, not their contents: one request whatever the number, the same
// shape as ListAccountChannels. Reserved ids — Watch Later and Liked videos —
// are dropped here rather than by the caller, because they are an artefact of
// how YouTube reports this page and nothing above should have to know that.
func (d *Downloader) ListAccountPlaylists(
	ctx context.Context, cookiesFile string,
) ([]domain.AccountPlaylist, error) {
	if cookiesFile == "" {
		return nil, domain.ErrNoAccount
	}

	result, err := newAccountCommand(cookiesFile).
		FlatPlaylist().
		DumpJSON().
		NoWarnings().
		Run(ctx, domain.FeedPlaylists)
	if err != nil {
		// Same reading as the channel list: the URL is a constant here, so a URL
		// that will not resolve is a session that has ended.
		if isAuthFailure(err) || strings.Contains(err.Error(), "Failed to resolve url") {
			return nil, fmt.Errorf("playlist list: %w", domain.ErrAccountAuth)
		}
		return nil, fmt.Errorf("playlist list: %w", err)
	}

	infos, err := result.GetExtractedInfo()
	if err != nil {
		return nil, err
	}

	var out []domain.AccountPlaylist
	add := func(id string, title *string) {
		if !domain.IsImportablePlaylist(id) {
			return
		}
		name := derefString(title)
		if name == "" {
			// A playlist with no name cannot be told from another on the page,
			// and there is nowhere else its identity could come from.
			return
		}
		out = append(out, domain.AccountPlaylist{ID: id, Title: name})
	}
	for _, info := range infos {
		if len(info.Entries) > 0 {
			for _, entry := range info.Entries {
				add(entry.ID, entry.Title)
			}
			continue
		}
		add(info.ID, info.Title)
	}
	return out, nil
}

// isAuthFailure tells a dead session apart from an ordinary refusal.
//
// The difference decides whether the account is retired, so it is drawn
// narrowly and on yt-dlp's own wording. A 403 is *not* in here: googlevideo
// refuses in waves — the same URL has answered 206, then 403, then 206 within
// an hour on this library — and reading that as "your session ended" would
// retire a working account on a bad afternoon.
func isAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, phrase := range []string{
		"sign in to confirm",
		"please sign in",
		"login required",
		// yt-dlp's actual wording for a signed-out request to a feed that needs
		// a session — measured against a real expired cookies.txt on ":ytsubs"
		// and ":ytfav". Without it a dead session was logged as an ordinary
		// failure, the account never expired, and the same dead cookie was
		// replayed every hour: §6b's rule about how a blocked address becomes a
		// banned account, arrived at through the one door left open.
		"login details are needed",
		"requires authentication",
		"cookies are no longer valid",
		"account cookies are invalid",
		"unable to log in",
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}
