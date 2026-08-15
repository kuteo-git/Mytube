package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/lucnguyen/local-youtube/services/catalog/internal/domain"
)

// stateRecordingRepo remembers the states that got past validation. Every other
// method is left to the embedded nil interface: reaching one is a test bug, and
// a panic says so more clearly than a zero value would.
type stateRecordingRepo struct {
	domain.Repository
	written []domain.MediaState
}

func (r *stateRecordingRepo) SetMediaState(
	_ context.Context, _ string, state domain.MediaState, _ string, _ int64, _ []domain.SubtitleTrack,
) error {
	r.written = append(r.written, state)
	return nil
}

// UNAVAILABLE was added to the domain, to the database's CHECK constraint and
// to every caller, but not to this switch — so ingest's report of a
// members-only video was rejected before it ever reached the database, and the
// log filled with `unknown media state "UNAVAILABLE"` while the catalogue went
// on offering the video as something that could still arrive.
func TestEveryDeclaredMediaStateIsAccepted(t *testing.T) {
	for _, state := range []domain.MediaState{
		domain.MediaQueued,
		domain.MediaDownloading,
		domain.MediaReady,
		domain.MediaEvicted,
		domain.MediaFailed,
		domain.MediaUnavailable,
	} {
		t.Run(string(state), func(t *testing.T) {
			repo := &stateRecordingRepo{}
			c := NewCatalog(repo, 0)

			if err := c.SetMediaState(context.Background(), "vid1", state, "", 0, nil); err != nil {
				t.Fatalf("SetMediaState(%q) = %v, want it accepted", state, err)
			}
			if len(repo.written) != 1 || repo.written[0] != state {
				t.Fatalf("repo saw %v, want [%q]", repo.written, state)
			}
		})
	}
}

// A state nothing declares is still refused: the switch is a guard, not a
// formality, and the database's CHECK constraint would reject it anyway with a
// far worse message.
func TestAnUndeclaredMediaStateIsRefused(t *testing.T) {
	repo := &stateRecordingRepo{}
	c := NewCatalog(repo, 0)

	err := c.SetMediaState(context.Background(), "vid1", domain.MediaState("SOMETHING_ELSE"), "", 0, nil)
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("err = %v, want it to wrap ErrInvalid", err)
	}
	if len(repo.written) != 0 {
		t.Fatalf("repo saw %v, want nothing written", repo.written)
	}
}
