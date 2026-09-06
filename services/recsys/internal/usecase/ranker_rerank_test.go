package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// countingStore records how often a ranking was built. BuildProfile is the
// first thing buildInputs asks for, so counting it counts ranks.
type countingStore struct {
	stubStore
	ranks int
}

func (s *countingStore) BuildProfile(
	ctx context.Context, userID string, window time.Duration,
) (domain.UserProfile, error) {
	s.ranks++
	return s.stubStore.BuildProfile(ctx, userID, window)
}

// The measured defect: every feed request ranked the whole library, whether or
// not there was anything new to rank. On this household's 43,079 videos that
// was ~700ms of a ~724ms request — paid on page two, page three and every
// pull-to-refresh, to append nothing at all.
func TestAnUnchangedLibraryIsNotRankedAgain(t *testing.T) {
	store := &countingStore{stubStore: stubStore{profile: emptyProfile()}}
	ranker := NewRanker(store, stubFeatures{features: features(50)})
	ranker.snapshots = NewSnapshotStore(time.Minute)

	ctx := context.Background()
	first, err := ranker.GetFeedPage(ctx, "user1", "", "", 20, 0, DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if store.ranks != 1 {
		t.Fatalf("first page ranked %d times, want 1", store.ranks)
	}

	for i := 0; i < 5; i++ {
		if _, err := ranker.GetFeedPage(
			ctx, "user1", "", first.SnapshotID, 20, int32(20*(i+1)), DefaultFeedMix, nil, Tuning{},
		); err != nil {
			t.Fatalf("page %d: %v", i+2, err)
		}
	}
	if store.ranks != 1 {
		t.Fatalf("five further pages ranked %d times in total, want 1", store.ranks)
	}

	// A tokenless request is what pull-to-refresh sends, and it joins the live
	// ordering rather than minting one — so it must not rank either.
	if _, err := ranker.GetFeedPage(
		ctx, "user1", "", "", 20, 0, DefaultFeedMix, nil, Tuning{},
	); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if store.ranks != 1 {
		t.Fatalf("a refresh over an unchanged library ranked %d times, want 1", store.ranks)
	}
}

// The other half of the same decision: material that arrived after the ordering
// was built still has to reach the tail, so a library that has moved must rank.
func TestAChangedLibraryIsRankedAgainAndAppended(t *testing.T) {
	store := &countingStore{stubStore: stubStore{profile: emptyProfile()}}
	ranker := NewRanker(store, stubFeatures{features: features(30)})
	ranker.snapshots = NewSnapshotStore(time.Minute)

	ctx := context.Background()
	first, err := ranker.GetFeedPage(ctx, "user1", "", "", 30, 0, DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(first.Videos) != 30 {
		t.Fatalf("page 1 returned %d, want 30", len(first.Videos))
	}

	ranker.features = stubFeatures{features: features(40)}
	grown, err := ranker.GetFeedPage(ctx, "user1", "", first.SnapshotID, 40, 0, DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("after ingest: %v", err)
	}
	if store.ranks != 2 {
		t.Fatalf("ranked %d times, want 2", store.ranks)
	}
	if len(grown.Videos) != 40 {
		t.Fatalf("the ten new videos did not reach the tail: got %d, want 40", len(grown.Videos))
	}

	// The first thirty keep the places they had. New material goes behind what
	// the viewer has already scrolled past, never into the middle of it.
	for i, v := range first.Videos {
		if grown.Videos[i].VideoID != v.VideoID {
			t.Fatalf("position %d changed from %s to %s", i, v.VideoID, grown.Videos[i].VideoID)
		}
	}
}

// A swap leaves the count where it was, which is why the fingerprint reads the
// ids rather than counting them.
func TestALibraryOfTheSameSizeButDifferentVideosIsRankedAgain(t *testing.T) {
	store := &countingStore{stubStore: stubStore{profile: emptyProfile()}}
	original := features(20)
	ranker := NewRanker(store, stubFeatures{features: original})
	ranker.snapshots = NewSnapshotStore(time.Minute)

	ctx := context.Background()
	first, err := ranker.GetFeedPage(context.Background(), "user1", "", "", 20, 0, DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}

	swapped := append([]domain.VideoFeatures(nil), original...)
	swapped[0].VideoID = "zz"
	ranker.features = stubFeatures{features: swapped}

	if _, err := ranker.GetFeedPage(
		ctx, "user1", "", first.SnapshotID, 20, 0, DefaultFeedMix, nil, Tuning{},
	); err != nil {
		t.Fatalf("after swap: %v", err)
	}
	if store.ranks != 2 {
		t.Fatalf("ranked %d times, want 2", store.ranks)
	}
}
