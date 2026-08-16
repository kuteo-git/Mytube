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
	if limit <= 0 || limit > 200 {
		limit = 50
	}

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
		if isAuthFailure(err) {
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
