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
	// makes them hours after an upload, so a later play asks again; not the
	// same play, whose caption fetch has just listed it.
	spokenNoneRest = 10 * time.Minute
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
	if err != nil {
		s.logger.Warn("spoken language", "video", videoID, "refused", refused, "error", err)
		if ctx.Err() != nil {
			// The asker gave up, which says nothing about the video.
			return ""
		}
	}
	s.Listed(ctx, videoID, lang, refused)
	return lang
}

// Listed takes what a listing said: the language is recorded; nothing, or a
// refusal, rests the video so the next play does not list it again at once.
// A caption fetch's listing reports here too.
func (s *SpokenLanguages) Listed(ctx context.Context, videoID, language string, refused bool) {
	if language != "" {
		if err := s.store.SetSpokenLanguage(context.WithoutCancel(ctx), videoID, language); err != nil {
			s.logger.Warn("record spoken language", "video", videoID, "error", err)
		}
		s.mu.Lock()
		delete(s.until, videoID)
		s.mu.Unlock()
		return
	}
	rest := spokenNoneRest
	if refused {
		rest = spokenRefusedRest
	}
	s.mu.Lock()
	s.until[videoID] = s.now().Add(rest)
	s.mu.Unlock()
}
