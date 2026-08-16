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
