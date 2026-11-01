package usecase

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// SpokenLister asks YouTube which language is spoken in a video.
type SpokenLister interface {
	SpokenLanguage(ctx context.Context, videoID string) (language string, refused bool, err error)
}

// SpokenStore records the answer where every player reads it.
type SpokenStore interface {
	SetSpokenLanguage(ctx context.Context, videoID, language string) error
}

const (
	// After a refusal the video is left alone for this long: the caption
	// endpoint refuses in waves, and asking through one is how a refusal
	// becomes a block.
	spokenRefusedRest = 10 * time.Minute
	// After any other listing without an answer — no automatic captions yet
	// (YouTube makes them hours after an upload), or a failure. Long enough
	// that the play which just listed it (its caption fetch, then the
	// player's ask) does not list it again; short enough that a later play
	// asks again.
	spokenNoneRest = 2 * time.Minute
	// spokenListTimeout bounds one listing: two requests, measured 0.7–1.1s.
	spokenListTimeout = 20 * time.Second
)

// SpokenLanguages answers which language is spoken in a video, for a player
// opening one nobody has asked about, and records what it hears.
//
// The answer is written to the catalogue and asked for no more. Nothing that
// is not an answer is written: the video rests, and a later play asks again.
type SpokenLanguages struct {
	lister SpokenLister
	store  SpokenStore
	logger *slog.Logger
	now    func() time.Time

	// One listing per video at a time, whoever asks.
	listing singleflight.Group

	mu    sync.Mutex
	until map[string]time.Time
}

func NewSpokenLanguages(lister SpokenLister, store SpokenStore, logger *slog.Logger) *SpokenLanguages {
	return &SpokenLanguages{lister: lister, store: store, logger: logger, now: time.Now, until: map[string]time.Time{}}
}

// Ask asks YouTube, unless the video is resting. It answers when the listing
// does or when the asker stops waiting, whichever is first; the listing runs
// on to its own bound either way and records what it finds, so an asker that
// gave up finds the answer in the catalogue next time.
func (s *SpokenLanguages) Ask(ctx context.Context, videoID string) string {
	if videoID == "" || s.resting(videoID) {
		return ""
	}
	answer := s.listing.DoChan(videoID, func() (any, error) {
		if s.resting(videoID) {
			// A listing that finished between the check above and here.
			return "", nil
		}
		lctx, cancel := context.WithTimeout(context.Background(), spokenListTimeout)
		defer cancel()
		lang, refused, err := s.lister.SpokenLanguage(lctx, videoID)
		if err != nil {
			s.logger.Warn("spoken language", "video", videoID, "refused", refused, "error", err)
		}
		s.Listed(lctx, videoID, lang, refused)
		return lang, nil
	})
	select {
	case r := <-answer:
		return r.Val.(string)
	case <-ctx.Done():
		return ""
	}
}

// Listed takes what a listing said: the language is recorded; no answer
// rests the video, longer after a refusal. A caption fetch's listing reports
// here too.
func (s *SpokenLanguages) Listed(ctx context.Context, videoID, language string, refused bool) {
	if language == "" {
		rest := spokenNoneRest
		if refused {
			rest = spokenRefusedRest
		}
		s.mu.Lock()
		s.until[videoID] = s.now().Add(rest)
		s.mu.Unlock()
		return
	}
	if err := s.store.SetSpokenLanguage(context.WithoutCancel(ctx), videoID, language); err != nil {
		s.logger.Warn("record spoken language", "video", videoID, "error", err)
	}
	s.mu.Lock()
	delete(s.until, videoID)
	s.mu.Unlock()
}

func (s *SpokenLanguages) resting(videoID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now().Before(s.until[videoID])
}
