package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/lucnguyen/local-youtube/services/catalog/internal/domain"
)

type recordingRepo struct {
	domain.Repository
	userID  string
	videoID string
	pinned  bool
	called  bool
}

func (r *recordingRepo) SetPinned(_ context.Context, userID, videoID string, pinned bool) error {
	r.userID, r.videoID, r.pinned, r.called = userID, videoID, pinned, true
	return nil
}

// Saving is one member's shelf, so a save with nobody attached is not a save
// with a default owner — it is a row nobody can see and nobody can take back,
// while still pinning the file against eviction for good.
func TestSavingWithoutAMemberIsRefused(t *testing.T) {
	repo := &recordingRepo{}
	c := &Catalog{repo: repo}

	err := c.SetPinned(context.Background(), "", "v1", true)
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("SetPinned with no user: got %v, want ErrInvalid", err)
	}
	if repo.called {
		t.Error("it reached the repository anyway")
	}
}

func TestSavingCarriesTheMemberThrough(t *testing.T) {
	repo := &recordingRepo{}
	c := &Catalog{repo: repo}

	if err := c.SetPinned(context.Background(), "u_lm", "v1", true); err != nil {
		t.Fatalf("SetPinned: %v", err)
	}
	if repo.userID != "u_lm" || repo.videoID != "v1" || !repo.pinned {
		t.Errorf("repository got (%q, %q, %v)", repo.userID, repo.videoID, repo.pinned)
	}
}
