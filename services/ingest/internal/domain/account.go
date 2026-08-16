package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A household member's YouTube account, as far as ingest is concerned.
//
// Which is: a cookie file, and what happened last time it was used. The account
// itself belongs to Google; this holds only the session somebody pasted and a
// note of whether it still works.
type Account struct {
	// UserID is the profile this account belongs to. Everything imported for it
	// is written under this id: subscriptions, likes, watch later.
	UserID string
	// Label is what the viewer called it, for the settings screen.
	Label      string
	AddedAt    time.Time
	LastScanAt time.Time
	// LastResult is a short human sentence, shown on the settings screen. It
	// must never contain anything read out of the cookie file.
	LastResult string
	State      AccountState
	// Failures in a row. Two is enough to stop using the account — see
	// accountFailureLimit.
	Failures int
}

type AccountState string

const (
	AccountOK       AccountState = "OK"
	AccountExpired  AccountState = "EXPIRED"
	AccountNeverSet AccountState = "NEVER_SET"
)

// ErrNoAccount is returned when a profile has no cookies stored.
var ErrNoAccount = errors.New("no account")

// ErrAccountAuth is upstream saying this session has ended.
//
// Distinct from every other failure on purpose. Only this one counts towards
// retiring an account: a 403 from googlevideo is an ordinary refusal that comes
// in waves, and treating it as an ended session would take a working account out
// of service on a bad afternoon.
var ErrAccountAuth = errors.New("account authentication failed")

// AccountStore keeps the cookie files and the little that is known about them.
//
// Deliberately narrow, and deliberately without a "read the cookies" method
// that anything but the yt-dlp call site can reach: the only thing that should
// ever hold this content is the command line being built for it. There is no
// path from the API to the bytes, so there is none to leak.
type AccountStore interface {
	// Save validates and writes. An invalid paste writes nothing.
	Save(ctx context.Context, userID, label, cookies string) error
	// Remove deletes the cookie file and forgets the account.
	Remove(ctx context.Context, userID string) error
	// List is the metadata only. Never the cookies.
	List(ctx context.Context) ([]Account, error)
	// CookiePath is where yt-dlp should read from, or ErrNoAccount.
	CookiePath(ctx context.Context, userID string) (string, error)
	// Record what a pass found, including whether the session still works.
	Record(ctx context.Context, userID string, result string, authFailed bool) error
}

// The feeds a signed-in member has, by yt-dlp's own aliases.
//
// Confirmed present in yt-dlp 2026.07.04 via --extractor-descriptions; the
// first three are listed there as "requires cookies", which is what makes them
// worth having and also what makes them the only requests here made as
// somebody.
//
// Watch history (":ythis") is deliberately absent. It is the most personal of
// these, and the catalogue already keeps its own history from what was actually
// played on this machine — importing a second one would mean holding a record
// of everything somebody watched anywhere, to no end this library has.
const (
	FeedSubscriptions = ":ytsubs"
	FeedLiked         = ":ytfav"
	FeedWatchLater    = ":ytwatchlater"
	FeedRecommended   = ":ytrec"
)

// FeedChannels is the member's subscription list itself, as opposed to
// ":ytsubs", which is the *uploads* of everything they follow.
//
// The difference is the whole of why this exists. ":ytsubs" is read from the
// top for what is new, so the channels it names are only those that have posted
// recently — a household member with 152 subscriptions produced 19, because the
// 50 most recent uploads came from 19 channels, and a channel that has not
// posted in a fortnight could never be imported at all.
//
// Not a yt-dlp alias: there is none for this page, so it is the URL. Verified
// against a real session — `yt-dlp --flat-playlist https://www.youtube.com/feed/channels`
// returns one entry per channel with `channel_id` set, 152 of them, in a single
// request.
const FeedChannels = "https://www.youtube.com/feed/channels"

// FeedPlaylists is the member's own playlist list.
//
// Like FeedChannels there is no yt-dlp alias for it, so it is the URL. Verified
// against a live session: 30 entries, one request, each carrying the playlist id
// as `id` and its name as `title`.
const FeedPlaylists = "https://www.youtube.com/feed/playlists"

// Playlist ids YouTube reports here that are not playlists in any sense this
// system uses. WL is Watch Later and LL is Liked videos: both already arrive
// through their own feeds, and Watch Later is deliberately not a playlist here
// at all — it has no name, cannot be created and cannot be deleted.
var reservedPlaylistIDs = map[string]bool{"WL": true, "LL": true}

// IsImportablePlaylist says whether a playlist id from the list is one this
// system should hold.
func IsImportablePlaylist(id string) bool {
	return id != "" && !reservedPlaylistIDs[id]
}

// AccountPlaylist is one playlist a member has.
type AccountPlaylist struct {
	ID    string
	Title string
}

// PlaylistURL is where its contents are read from, and the identity a stored
// playlist carries in source_url.
func PlaylistURL(id string) string {
	return "https://www.youtube.com/playlist?list=" + id
}

// AccountChannel is one channel a member follows.
type AccountChannel struct {
	ID   string
	Name string
}

// PlaylistGone is upstream's own answer that a playlist it listed cannot be
// read: "YouTube said: The playlist does not exist".
//
// Reproduced with a live session, minutes apart, on 10 of this household's 27
// playlists — so it is a property of the playlist rather than a bad moment.
// Matched narrowly and on YouTube's own wording, the same discipline
// ClassifyUnavailable follows for videos: anything vaguer would bury a readable
// playlist on a network error.
func PlaylistGone(err error) bool {
	return err != nil && strings.Contains(err.Error(), "The playlist does not exist")
}

// AccountFeedSource reads one of those feeds as the account whose cookies these
// are.
type AccountFeedSource interface {
	ListAccountFeed(ctx context.Context, cookiesFile, feed string, limit int32) ([]ExternalVideo, error)
	// ListAccountPlaylists reads the member's playlist list — the lists
	// themselves, not their contents.
	ListAccountPlaylists(ctx context.Context, cookiesFile string) ([]AccountPlaylist, error)
	// ListAccountChannels reads the whole subscription list, not a page of it.
	// Unlike the video feeds it is not capped: it is one request whatever the
	// length, and a partial list here is not "the newest few" but an arbitrary
	// subset of who somebody follows.
	ListAccountChannels(ctx context.Context, cookiesFile string) ([]AccountChannel, error)
}

// SignalSink tells the ranker what a member's account says they like.
//
// Separate from the Library on purpose, because they are two different records
// of the same fact and both have to be written. `catalog.subscriptions` is the
// authoritative list the Subscriptions page shows; `recsys.signals` is what
// ranking actually reads. Writing only the first is what made an imported
// account's whole feed read "Suggested video": every channel it followed looked
// unsubscribed to the thing doing the ranking.
type SignalSink interface {
	Subscribed(ctx context.Context, userID, channelID string, occurredAt time.Time) error
	Liked(ctx context.Context, userID, videoID string, occurredAt time.Time) error
}

// How many authentication failures in a row before an account is left alone.
//
// Two. One is ordinary — YouTube refuses things in waves, and this library has
// measured the same URL answering 206, then 403, then 206 within the hour. Two
// in a row is a session that has actually ended, and replaying a dead session
// hourly is how an address stops being merely blocked (§8, risk 6) and an
// account starts being banned.
const AccountFailureLimit = 2

// ValidateCookies checks a paste before any of it is written.
//
// Nothing is stored until this passes, and the reason is written in
// session.go: "a cookies file that is not there is worse than none" — yt-dlp
// fails the request outright rather than carrying on without it. A file that is
// there but is nonsense is worse still, because it also looks configured.
func ValidateCookies(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("%w: nothing pasted", ErrInvalid)
	}

	lines := strings.Split(trimmed, "\n")
	first := strings.TrimSpace(lines[0])
	// yt-dlp's own requirement, and the one thing that tells a Netscape export
	// apart from the JSON the same extensions also offer.
	if !strings.HasPrefix(first, "# HTTP Cookie File") &&
		!strings.HasPrefix(first, "# Netscape HTTP Cookie File") {
		return fmt.Errorf("%w: this does not look like a cookies.txt — the first line must say "+
			"\"# HTTP Cookie File\" or \"# Netscape HTTP Cookie File\"", ErrInvalid)
	}

	var youtube, rows int
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// domain, includeSubdomains, path, secure, expiry, name, value
		if fields := strings.Split(line, "\t"); len(fields) >= 6 {
			rows++
			if strings.Contains(fields[0], "youtube.com") {
				youtube++
			}
		}
	}

	if rows == 0 {
		return fmt.Errorf("%w: no cookies in the file", ErrInvalid)
	}
	if youtube == 0 {
		return fmt.Errorf("%w: no youtube.com cookies — export them from a tab that is "+
			"signed in to YouTube", ErrInvalid)
	}
	return nil
}
