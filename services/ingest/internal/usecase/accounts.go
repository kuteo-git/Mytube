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
	Expired       int
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
		if authFailed {
			out.Expired++
		}

		// Never the account's own details in the note: this string is shown on
		// a settings screen and written to a file beside the cookies.
		note := fmt.Sprintf("%d subscriptions, %d videos", result.Subscriptions, result.Videos)
		if authFailed {
			note = "signed out — paste your cookies again"
		}
		if err := s.accounts.Record(ctx, account.UserID, note, authFailed); err != nil {
			s.logger.Warn("recording account scan", "user", account.UserID, "error", err)
		}
	}
	return out, nil
}

func (s *AccountScanner) scanOne(ctx context.Context, userID, cookiePath string) (AccountScanResult, bool) {
	var out AccountScanResult

	type feedSpec struct {
		name string
		// via is the provenance written on anything this feed brings in.
		via string
		// subscribe records the channel as followed by this member.
		subscribe bool
		// like records the video as liked by this member.
		like bool
	}

	// Order is deliberate: what the member chose first, what YouTube guessed
	// last. If a session dies partway through, the passes that mattered have
	// already run.
	feeds := []feedSpec{
		{name: domain.FeedSubscriptions, via: "SOURCE", subscribe: true},
		{name: domain.FeedLiked, via: "SOURCE", like: true},
		{name: domain.FeedWatchLater, via: "SOURCE"},
		{name: domain.FeedRecommended, via: "YOUTUBE_REC"},
	}

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

			if feed.subscribe && v.ChannelID != "" {
				if err := s.library.SetSubscription(ctx, userID, v.ChannelID, true); err != nil {
					s.logger.Warn("subscribe", "user", userID, "channel", v.ChannelID, "error", err)
					continue
				}
				// And the ranker, which keeps its own record and would
				// otherwise go on believing this member follows nobody.
				s.tellRanker(ctx, userID, v.ChannelID, true)
				out.Subscriptions++
			}
			if feed.like {
				if err := s.library.SetLiked(ctx, userID, v.ID); err != nil {
					s.logger.Warn("like", "user", userID, "video", v.ID, "error", err)
					continue
				}
				s.tellRanker(ctx, userID, v.ID, false)
			}
		}
	}
	return out, false
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
