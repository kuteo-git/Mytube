package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
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

// How many playlists one pass reads, and how deep into each.
//
// Two different numbers, because they answer two different questions.
//
// A playlist nobody has read yet is empty, and an empty playlist reads as
// broken. Filling the library happens once, so it is not the traffic the
// credential rule is about — that rule is about what this does *every hour*.
// So the first fill takes them all (up to firstFillLimit, which exists only so
// a member with hundreds does not do hundreds in one go), spaced by the same
// pause as every other account request.
//
// Re-reading is the hourly part, and that is where the small number belongs:
// four a pass keeps a household's playlists honest for four requests an hour.
//
// playlistItemLimit is deliberately *not* accountFeedLimit, which is where it
// started and where it was wrong.
//
// Fifty is right for a feed — a subscription or recommendation feed is read from
// the top for what is new, and walking it to the end every hour would be the
// high-volume traffic the credential rule exists to avoid. A playlist is not a
// feed. It is a finite list somebody assembled, and half of one is not a smaller
// version of it: this household's "Luke Music" holds 139 videos and arrived with
// 50.
//
// It also cost nothing to fix, which is the part I should have checked first.
// Reading a playlist is one yt-dlp invocation whatever the depth — the
// pagination is internal — and measured on that same playlist: 50 entries in 1s,
// all 139 in 2s.
//
// And it quietly broke the mirror. `complete` is "did the read see the whole
// list", computed as fewer entries than the cap, so at 50 every playlist longer
// than 50 was permanently "read in part" and could never have anything removed
// from it.
//
// Still bounded, because §8 says a pass is always bounded. Two thousand, from
// the sizes actually here: 1186 and 776 are real playlists in this house, and
// 500 cut both. The cost is a page fetch per hundred entries rather than a
// request per video — 1186 read in 8s, one invocation — so the bound is about
// not walking away with somebody's 20,000-video music dump, not about the
// hourly traffic the credential rule guards.
const (
	playlistRereadsPerPass = 4
	firstFillLimit         = 60
	playlistItemLimit      = 2000
)

// How many failures one pass will report on the settings screen.
//
// A pass that fails on every one of a hundred playlists must not grow a
// hundred-line message; the log has all of them.
const maxScanErrors = 10

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

	// What the pass in flight is doing, for the settings screen.
	//
	// Held here rather than returned, because a pass now outlives the request
	// that started it: a first fill reads every playlist and takes minutes, and
	// a browser that reloads in the middle must be able to ask again rather than
	// lose it. Same shape as the topic scanner's LastScan for the same reason.
	mu     sync.Mutex
	status domain.AccountScanStatus

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

// Status is what the pass in flight is doing, or what the last one did.
//
// Held on the server rather than returned to whoever pressed the button: a first
// fill reads every playlist a member has and takes minutes, so the pass outlives
// the request that started it, and a browser that reloads has to be able to ask
// again rather than lose sight of it. Same shape as the topic scanner's
// LastScan, for the same reason.
//
// In memory, deliberately. A pass cannot survive a restart, so neither should
// the claim that one is running.
func (s *AccountScanner) Status() domain.AccountScanStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.status
	if out.Running {
		out.DurationMs = time.Since(out.StartedAt).Milliseconds()
	}
	// Copied: the caller is a request goroutine and the pass is still writing.
	out.Errors = append([]string(nil), s.status.Errors...)
	return out
}

func (s *AccountScanner) setPhase(phase string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Phase = phase
}

// noteError records a failure that did not stop the pass.
//
// Bounded: a pass that fails on every one of a hundred playlists must not grow a
// hundred-line message on a settings screen.
func (s *AccountScanner) noteError(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.status.Errors) < maxScanErrors {
		s.status.Errors = append(s.status.Errors, text)
	}
}

func (s *AccountScanner) setPlaylistProgress(read, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.PlaylistsRead, s.status.PlaylistsTotal = read, total
	if total > 0 {
		s.status.Phase = fmt.Sprintf("reading playlists (%d of %d)", read, total)
	}
}

// ScanAll reads every account that still has a working session.
//
// onlyUser limits the pass to one member; empty means the whole household. The
// timer passes empty; the Scan now button passes whoever pressed it, because
// that button sits on a screen about *your* account and scanning everybody from
// it is a surprise.
func (s *AccountScanner) ScanAll(ctx context.Context, onlyUser string) (AccountScanResult, error) {
	var out AccountScanResult
	if s.accounts == nil || s.feeds == nil {
		return out, nil
	}
	if s.running {
		return out, nil
	}
	s.running = true

	s.mu.Lock()
	started := time.Now()
	s.status = domain.AccountScanStatus{Running: true, StartedAt: started, Phase: "starting"}
	s.mu.Unlock()

	defer func() {
		s.running = false
		s.mu.Lock()
		s.status.Running = false
		s.status.Phase = ""
		s.status.DurationMs = time.Since(started).Milliseconds()
		s.status.Accounts = out.Accounts
		s.status.Subscriptions = out.Subscriptions
		s.status.Playlists = out.Playlists
		s.status.Videos = out.Videos
		s.status.PlaylistVideos = out.PlaylistVideos
		s.status.Expired = out.Expired
		s.mu.Unlock()
	}()

	list, err := s.accounts.List(ctx)
	if err != nil {
		// Noted as well as returned: the pass runs detached from whoever started
		// it, so the status is the only place this can be seen.
		s.noteError("could not read the list of accounts")
		return out, err
	}

	for _, account := range list {
		if onlyUser != "" && account.UserID != onlyUser {
			continue
		}
		if account.State == domain.AccountExpired {
			out.Expired++
			continue
		}
		path, err := s.accounts.CookiePath(ctx, account.UserID)
		if err != nil {
			continue
		}
		out.Accounts++
		// No account details in the phase: it is shown on a settings screen, the
		// same rule the recorded note follows.
		s.setPhase("reading subscriptions and feeds")

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
			if err := s.library.UpsertVideo(ctx, v, "ABSENT"); err != nil {
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

// syncPlaylistItems reads playlist contents: everything never read, then a few
// of the ones read longest ago.
//
// Two lists with two budgets, because they are two different costs. Filling the
// library happens **once** and an unread playlist is an empty page with a title,
// so the first fill takes them all — 28 requests three seconds apart is shorter
// and slower than an ordinary hour of the anonymous scanner. Re-reading is the
// part that repeats every hour, and that is where the small number belongs.
//
// Deliberately outside the per-member loop: which playlists are due is a
// question about all of them at once, and asking it per member would let a
// household of four spend four times the requests on the same budget.
func (s *AccountScanner) syncPlaylistItems(ctx context.Context, out *AccountScanResult) {
	unread, err := s.library.ListUnreadPlaylists(ctx, firstFillLimit)
	if err != nil {
		s.logger.Warn("list unread playlists", "error", err)
		s.noteError("could not list playlists to read")
	}
	stale, err := s.library.ListStalePlaylists(ctx, playlistRereadsPerPass)
	if err != nil {
		s.logger.Warn("list stale playlists", "error", err)
		s.noteError("could not list playlists to refresh")
	}

	// Unread first: an empty playlist is the thing somebody is looking at right
	// now, and a refresh of one that already has videos can wait.
	due := append(unread, stale...)

	// Counted, not indexed. The loop skips a playlist whose owner has no working
	// session, and counting the index called that progress: a pass that read
	// nothing at all reported "16 of 17" and looked like it had worked.
	read, skipped := 0, 0
	s.setPlaylistProgress(0, len(due))

	for _, p := range due {
		cookiePath, err := s.accounts.CookiePath(ctx, p.UserID)
		if err != nil {
			// The member whose playlist this is has no working session. Skipped
			// rather than read anonymously: a playlist can be private, and asking
			// for one without the account that owns it is a request that fails
			// and teaches nothing.
			skipped++
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
			// Upstream's own answer, remembered once rather than asked for ever.
			// Ten of this household's twenty-seven playlists are listed on
			// /feed/playlists and refuse to be read; without this each costs a
			// request every pass and sits at the front of the unread queue ahead
			// of playlists that could have been read.
			if domain.PlaylistGone(err) {
				if err := s.library.MarkPlaylistUnavailable(ctx, p.ID, p.UserID); err != nil {
					s.logger.Warn("mark playlist unavailable", "playlist", p.ID, "error", err)
				}
				s.logger.Info("playlist unreadable upstream", "user", p.UserID, "playlist", p.ID)
				continue
			}
			s.logger.Warn("read playlist", "user", p.UserID, "playlist", p.ID, "error", err)
			s.noteError("a playlist could not be read")
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
			if err := s.library.UpsertVideo(ctx, v, "ABSENT"); err != nil {
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
		read++
		s.setPlaylistProgress(read, len(due)-skipped)
	}

	// Said plainly rather than left as silence. A pass that skipped everything
	// for want of a session looked, from the settings screen, exactly like a
	// pass that had nothing to do.
	if skipped > 0 && read == 0 {
		s.noteError("no working YouTube session — playlists were not read")
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
		// The timer scans the whole household; only the button is per member.
		if result, err := s.ScanAll(ctx, ""); err != nil {
			s.logger.Warn("scheduled account scan", "error", err)
		} else if result.Accounts > 0 {
			s.logger.Info("account scan", "accounts", result.Accounts,
				"subscriptions", result.Subscriptions, "playlists", result.Playlists,
				"videos", result.Videos, "playlist videos", result.PlaylistVideos,
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
