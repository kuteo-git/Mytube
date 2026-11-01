package usecase

import (
	"context"
	"log/slog"
	"sync"
	"time"
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
	// After YouTube refuses, the video is left alone for this long: the
	// caption endpoint refuses in waves, and asking through one is how a
	// refusal becomes a block.
	spokenRefusedRest = 10 * time.Minute
	// After an answer of "none" — no automatic captions yet, or ever. YouTube
	// makes them hours after an upload, so the question is asked again, but
	// not on every play.
	spokenNoneRest = time.Hour
)

// SpokenLanguages answers which language is spoken in a video, for a player
// opening one nobody has asked about, and records what it hears.
//
// The answer is written to the catalogue and asked for no more. Nothing that
// is not an answer is written: a refusal or an empty listing is asked again
// on a later play, after a rest.
type SpokenLanguages struct {
	lister SpokenLister
	store  SpokenStore
	logger *slog.Logger
	now    func() time.Time

	mu    sync.Mutex
	until map[string]time.Time
}

func NewSpokenLanguages(lister SpokenLister, store SpokenStore, logger *slog.Logger) *SpokenLanguages {
	return &SpokenLanguages{lister: lister, store: store, logger: logger, now: time.Now, until: map[string]time.Time{}}
}

// Ask asks YouTube, unless it was asked about this video too recently.
func (s *SpokenLanguages) Ask(ctx context.Context, videoID string) string {
	s.mu.Lock()
	resting := s.now().Before(s.until[videoID])
	s.mu.Unlock()
	if resting || videoID == "" {
		return ""
	}
	lang, refused, err := s.lister.SpokenLanguage(ctx, videoID)
	if lang != "" {
		s.Heard(ctx, videoID, lang)
		return lang
	}
	rest := spokenNoneRest
	if refused {
		rest = spokenRefusedRest
	}
	if err != nil {
		s.logger.Warn("spoken language", "video", videoID, "refused", refused, "error", err)
	}
	s.mu.Lock()
	s.until[videoID] = s.now().Add(rest)
	s.mu.Unlock()
	return ""
}

// Heard records the language spoken in a video, from any listing that showed it.
func (s *SpokenLanguages) Heard(ctx context.Context, videoID, language string) {
	if err := s.store.SetSpokenLanguage(context.WithoutCancel(ctx), videoID, language); err != nil {
		s.logger.Warn("record spoken language", "video", videoID, "error", err)
	}
}
