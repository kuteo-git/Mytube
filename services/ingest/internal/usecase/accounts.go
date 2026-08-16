package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// Reading each household member's own YouTube feeds, as them.
//
// This is the only traffic in the system that carries a session, and everything
// about its shape is an answer to that. §8's risk 6 is that too many requests
// get this *address* blocked; an account raises the stakes to the account
// itself, which is not something a re-scan can undo.
//
// So: one pass an hour for everybody together, a handful of requests each,
// serial with a pause between them, and an account that fails authentication
// twice stops being used until somebody pastes a fresh session. The anonymous
// scanner is untouched and keeps its own schedule — the two are separate
// precisely so this one can be switched off on the day an account looks watched.

// How many videos to take from each feed.
//
// The top of the list, not the whole of it. A subscription feed is read for what
// is new, and walking it to the end every hour would turn five requests into
// fifty — the high-volume traffic the credential rule exists to keep sessions
// away from.
const accountFeedLimit = 50

// Between one account's requests and the next.
const accountRequestPause = 3 * time.Second

// How many playlists' contents one pass reads, and how deep into each.
//
// Four an hour walks thirty playlists in under a day, at four requests a pass
// on the one session that carries a name. The depth matches accountFeedLimit
// for the same reason: this is read for what a playlist mostly is, not to mirror
// a thousand-video list into the library.
const (
	playlistsPerPass  = 4
	playlistItemLimit = 50
)

// AccountScanner reads household members' own feeds.
type AccountScanner struct {
	accounts domain.AccountStore
	feeds    domain.AccountFeedSource
	library  domain.Library
	// Where ranking learns about it. Optional: without one the import still
	// fills the library and the Subscriptions page, and only the feed stays
	// ignorant — which is exactly the half-working state this exists to close.
	signals domain.SignalSink
	logger  *slog.Logger

	// Gap between requests. Zero means the package default; tests set it so
	// they do not wait out a pause that exists for YouTube.
	pause time.Duration

	// One pass at a time. Two would double the request rate against every
	// account at once, which is the one thing this is arranged to avoid.
	running bool
}

func NewAccountScanner(
	accounts domain.AccountStore,
	feeds domain.AccountFeedSource,
	library domain.Library,
	signals domain.SignalSink,
	logger *slog.Logger,
) *AccountScanner {
	if logger == nil {
		logger = slog.Default()
	}
	return &AccountScanner{
		accounts: accounts, feeds: feeds, library: library,
		signals: signals, logger: logger,
	}
}

// AccountScanResult is what one pass did, for the settings screen.
type AccountScanResult struct {
	Accounts      int
	Subscriptions int
	Videos        int
	// Playlists named this pass, which is every one of them; PlaylistVideos is
	// what the few whose contents were read this pass contributed.
	Playlists      int
	PlaylistVideos int
	Expired        int
}

// ScanAll reads every account that still has a working session.
func (s *AccountScanner) ScanAll(ctx context.Context) (AccountScanResult, error) {
	var out AccountScanResult
	if s.accounts == nil || s.feeds == nil {
		return out, nil
	}
	if s.running {
		return out, nil
	}
	s.running = true
	defer func() { s.running = false }()

	list, err := s.accounts.List(ctx)
	if err != nil {
		return out, err
	}

	for _, account := range list {
		if account.State == domain.AccountExpired {
			out.Expired++
			continue
		}
		path, err := s.accounts.CookiePath(ctx, account.UserID)
		if err != nil {
			continue
		}
		out.Accounts++

		result, authFailed := s.scanOne(ctx, account.UserID, path)
		out.Subscriptions += result.Subscriptions
		out.Videos += result.Videos
		out.Playlists += result.Playlists
		if authFailed {
			out.Expired++
		}

		// Never the account's own details in the note: this string is shown on
		// a settings screen and written to a file beside the cookies.
		note := fmt.Sprintf("%d subscriptions, %d playlists, %d videos",
			result.Subscriptions, result.Playlists, result.Videos)
		if authFailed {
			note = "signed out — paste your cookies again"
		}
		if err := s.accounts.Record(ctx, account.UserID, note, authFailed); err != nil {
			s.logger.Warn("recording account scan", "user", account.UserID, "error", err)
		}
	}

	// Last, and across everybody: the stalest playlists in the house, not the
	// stalest of each member in turn.
	s.syncPlaylistItems(ctx, &out)
	return out, nil
}

func (s *AccountScanner) scanOne(ctx context.Context, userID, cookiePath string) (AccountScanResult, bool) {
	var out AccountScanResult

	type feedSpec struct {
		name string
		// via is the provenance written on anything this feed brings in.
		via string
		// like records the video as liked by this member.
		like bool
		// watchLater puts the video on this member's Watch Later list.
		//
		// The feed was read from the day this was written and its videos were
		// added to the library as ordinary ones, so a list somebody had built
		// deliberately arrived as an anonymous handful of new videos.
		watchLater bool
	}

	// Who the member follows, from the list itself rather than inferred from
	// whoever happened to post lately. See domain.FeedChannels: the uploads feed
	// named 19 channels for a member who follows 152.
	//
	// First, and its failure does not stop the rest: a member's subscriptions are
	// the whole reason their Home feed has anything in it, and the video passes
	// below still fill the library if this one is refused.
	if !s.importSubscriptions(ctx, userID, cookiePath, &out) {
		return out, true
	}

	// The member's playlists, by name. Their contents are read separately and a
	// few at a time — see syncPlaylistItems.
	if !s.importPlaylists(ctx, userID, cookiePath, &out) {
		return out, true
	}

	// Order is deliberate: what the member chose first, what YouTube guessed
	// last. If a session dies partway through, the passes that mattered have
	// already run.
	feeds := []feedSpec{
		{name: domain.FeedSubscriptions, via: "SOURCE"},
		{name: domain.FeedLiked, via: "SOURCE", like: true},
		{name: domain.FeedWatchLater, via: "SOURCE", watchLater: true},
		{name: domain.FeedRecommended, via: "YOUTUBE_REC"},
	}

	// Filled by the Watch later pass and written once it has finished: the
	// mirror needs the whole read at once to know what has left the list.
	var (
		watchLaterIDs      []string
		watchLaterComplete bool
		watchLaterRead     bool
	)

	for i, feed := range feeds {
		if i > 0 {
			select {
			case <-ctx.Done():
				return out, false
			case <-time.After(s.requestPause()):
			}
		}

		videos, err := s.feeds.ListAccountFeed(ctx, cookiePath, feed.name, accountFeedLimit)
		if err != nil {
			if errors.Is(err, domain.ErrAccountAuth) {
				// Stop at the first sign the session has ended rather than
				// working through the rest of the list. Four more refused
				// requests teach nothing and are four more marks against this
				// account.
				s.logger.Warn("account session refused", "user", userID, "feed", feed.name)
				return out, true
			}
			s.logger.Warn("account feed", "user", userID, "feed", feed.name, "error", err)
			continue
		}

		if feed.watchLater {
			watchLaterRead = true
			// A read that came back at the cap is a first page, not a list.
			watchLaterComplete = len(videos) < accountFeedLimit
		}

		for _, v := range videos {
			if v.ID == "" || v.SourceURL == "" {
				continue
			}
			v.DiscoveredVia = feed.via
			if err := s.library.UpsertChannel(ctx, v); err != nil {
				s.logger.Warn("upsert channel", "video", v.ID, "error", err)
				continue
			}
			if err := s.library.UpsertVideo(ctx, v, "QUEUED"); err != nil {
				s.logger.Warn("upsert video", "video", v.ID, "error", err)
				continue
			}
			out.Videos++

			if feed.like {
				if err := s.library.SetLiked(ctx, userID, v.ID); err != nil {
					s.logger.Warn("like", "user", userID, "video", v.ID, "error", err)
					continue
				}
				s.tellRanker(ctx, userID, v.ID, false)
			}
			// Collected rather than written one at a time, and never told to the
			// ranker: Watch later is a note about what to do next, not a
			// statement about taste.
			if feed.watchLater {
				watchLaterIDs = append(watchLaterIDs, v.ID)
			}
		}
	}

	// The mirror, written once. Skipped when the feed was never reached: an
	// empty list from a pass that never got there reads as an emptied one.
	if watchLaterRead && len(watchLaterIDs) > 0 {
		if err := s.library.ImportWatchLater(
			ctx, userID, watchLaterIDs, watchLaterComplete); err != nil {
			s.logger.Warn("import watch later", "user", userID, "error", err)
		}
	}
	return out, false
}

// importSubscriptions records everyone this member follows, and returns false
// only when the session itself has died — the one condition that must stop the
// pass rather than be stepped over.
//
// Additive: a channel missing from the list is left subscribed here. YouTube
// answering with a short list, a page it did not finish, or nothing at all would
// otherwise unsubscribe a member from everything in one pass, and ranking reads
// that record — a bad minute upstream would empty somebody's Home feed with no
// trace of why. Unsubscribing stays a thing done in this app, on purpose.
func (s *AccountScanner) importSubscriptions(
	ctx context.Context, userID, cookiePath string, out *AccountScanResult,
) bool {
	channels, err := s.feeds.ListAccountChannels(ctx, cookiePath)
	if err != nil {
		if errors.Is(err, domain.ErrAccountAuth) {
			s.logger.Warn("account session refused", "user", userID, "feed", "channels")
			return false
		}
		s.logger.Warn("account subscription list", "user", userID, "error", err)
		return true
	}

	// Counted once per channel. This used to be incremented per *video* in the
	// uploads feed, so a scan that found 19 channels reported 49 subscriptions
	// and read as though thirty had been lost.
	seen := map[string]bool{}
	for _, c := range channels {
		if c.ID == "" || seen[c.ID] {
			continue
		}
		seen[c.ID] = true

		// The channel row has to exist before anything can point at it, and the
		// list carries the name, which is all the Subscriptions page needs until
		// a scan fills in the artwork.
		if err := s.library.UpsertChannel(ctx, domain.ExternalVideo{
			ChannelID: c.ID, ChannelName: c.Name,
		}); err != nil {
			s.logger.Warn("upsert channel", "user", userID, "channel", c.ID, "error", err)
			continue
		}
		if err := s.library.SetSubscription(ctx, userID, c.ID, true); err != nil {
			s.logger.Warn("subscribe", "user", userID, "channel", c.ID, "error", err)
			continue
		}
		// And the ranker, which keeps its own record and would otherwise go on
		// believing this member follows nobody.
		s.tellRanker(ctx, userID, c.ID, true)
		out.Subscriptions++
	}
	return true
}

// importPlaylists records the member's playlist list by name, and reads the
// contents of a few of them.
//
// The split is what keeps this affordable. The list is one request; each
// playlist's contents is another, and this member has thirty. Reading them all
// every hour would put thirty named requests an hour against the address §8's
// risk 6 is about — so the list is refreshed every pass and the contents are
// taken a few at a time, stalest first, which walks the whole set in a day and
// costs four requests a pass.
//
// Returns false only when the session has died.
func (s *AccountScanner) importPlaylists(
	ctx context.Context, userID, cookiePath string, out *AccountScanResult,
) bool {
	lists, err := s.feeds.ListAccountPlaylists(ctx, cookiePath)
	if err != nil {
		if errors.Is(err, domain.ErrAccountAuth) {
			s.logger.Warn("account session refused", "user", userID, "feed", "playlists")
			return false
		}
		s.logger.Warn("account playlist list", "user", userID, "error", err)
		return true
	}

	keep := make([]string, 0, len(lists))
	for _, list := range lists {
		url := domain.PlaylistURL(list.ID)
		if _, err := s.library.UpsertPlaylist(ctx, userID, url, list.Title); err != nil {
			s.logger.Warn("upsert playlist", "user", userID, "playlist", list.ID, "error", err)
			continue
		}
		keep = append(keep, url)
		out.Playlists++
	}

	// The other half of the mirror. Without it a playlist deleted on YouTube
	// stays here for ever, because nothing in this app can delete one — the trap
	// a read-only mirror sets for itself. An empty answer prunes nothing: it is
	// far likelier to be a refusal than an account with no playlists left.
	if len(keep) > 0 {
		if err := s.library.PruneImportedPlaylists(ctx, userID, keep); err != nil {
			s.logger.Warn("prune playlists", "user", userID, "error", err)
		}
	}
	return true
}

// syncPlaylistItems reads the contents of the playlists nobody has looked at in
// longest, and appends what it finds.
//
// Deliberately outside the per-member loop: which playlists are stalest is a
// question about all of them at once, and asking it per member would let a
// household of four spend four times the requests on the same budget.
func (s *AccountScanner) syncPlaylistItems(ctx context.Context, out *AccountScanResult) {
	stale, err := s.library.ListStalePlaylists(ctx, playlistsPerPass)
	if err != nil {
		s.logger.Warn("list stale playlists", "error", err)
		return
	}

	for _, p := range stale {
		cookiePath, err := s.accounts.CookiePath(ctx, p.UserID)
		if err != nil {
			// The member whose playlist this is has no working session. Skipped
			// rather than read anonymously: a playlist can be private, and asking
			// for one without the account that owns it is a request that fails
			// and teaches nothing.
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(s.requestPause()):
		}

		// Read as the member, not anonymously. A playlist can be private, and
		// §6b's rule is narrow rather than absent: listings carry no credentials,
		// except the ones that are a member reading their own account — which a
		// private playlist plainly is. ListAccountFeed takes a URL as readily as
		// an alias, so this is the same call the other feeds make.
		videos, err := s.feeds.ListAccountFeed(ctx, cookiePath, p.SourceURL, playlistItemLimit)
		if err != nil {
			s.logger.Warn("read playlist", "user", p.UserID, "playlist", p.ID, "error", err)
			continue
		}

		ids := make([]string, 0, len(videos))
		for _, v := range videos {
			if v.ID == "" || v.SourceURL == "" {
				continue
			}
			// The video has to exist before the playlist can point at it, and a
			// playlist is often the first place a video is seen.
			if err := s.library.UpsertChannel(ctx, v); err != nil {
				s.logger.Warn("upsert channel", "video", v.ID, "error", err)
				continue
			}
			v.DiscoveredVia = "SOURCE"
			if err := s.library.UpsertVideo(ctx, v, "QUEUED"); err != nil {
				s.logger.Warn("upsert video", "video", v.ID, "error", err)
				continue
			}
			ids = append(ids, v.ID)
		}

		// Whether the whole playlist was seen. A read that came back at the cap
		// is a first page, not a list, and the mirror must not delete what it
		// never looked at.
		complete := len(videos) < playlistItemLimit
		if err := s.library.ImportPlaylistItems(ctx, p.ID, p.UserID, ids, complete); err != nil {
			s.logger.Warn("import playlist items", "playlist", p.ID, "error", err)
			continue
		}
		out.PlaylistVideos += len(ids)
	}
}

// Run scans on a timer. A zero interval disables it.
//
// Separate from the anonymous scanner's schedule, and that separation is the
// point: this is the traffic that carries a name, and it must be possible to
// stop it on its own without stopping the library from being scanned at all.
func (s *AccountScanner) Run(ctx context.Context, initialDelay, interval time.Duration) {
	if interval <= 0 {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	for {
		if result, err := s.ScanAll(ctx); err != nil {
			s.logger.Warn("scheduled account scan", "error", err)
		} else if result.Accounts > 0 {
			s.logger.Info("account scan", "accounts", result.Accounts,
				"subscriptions", result.Subscriptions, "videos", result.Videos,
				"expired", result.Expired)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// tellRanker records the same fact in recsys's own terms.
//
// Logged and stepped over rather than returned: a lost signal degrades ranking
// slightly, and it must never take down an import that has already written the
// authoritative row.
func (s *AccountScanner) tellRanker(ctx context.Context, userID, target string, subscribe bool) {
	if s.signals == nil {
		return
	}
	var err error
	if subscribe {
		err = s.signals.Subscribed(ctx, userID, target, time.Now())
	} else {
		err = s.signals.Liked(ctx, userID, target, time.Now())
	}
	if err != nil {
		s.logger.Warn("record account signal", "user", userID, "target", target, "error", err)
	}
}

func (s *AccountScanner) requestPause() time.Duration {
	if s.pause > 0 {
		return s.pause
	}
	return accountRequestPause
}
