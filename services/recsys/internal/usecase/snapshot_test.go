package usecase

import (
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

func ranked(ids ...string) []domain.RankedVideo {
	out := make([]domain.RankedVideo, 0, len(ids))
	for _, id := range ids {
		out = append(out, domain.RankedVideo{VideoID: id})
	}
	return out
}

func TestSnapshotReturnsTheSameOrderAcrossReads(t *testing.T) {
	store := NewSnapshotStore(time.Minute)
	id := store.Put("user1|Tech", ranked("a", "b", "c"))

	first, ok := store.Get(id)
	if !ok {
		t.Fatal("snapshot missing immediately after Put")
	}
	second, _ := store.Get(id)

	if len(first) != 3 || first[0].VideoID != "a" || second[2].VideoID != "c" {
		t.Fatalf("order changed between reads: %v then %v", first, second)
	}
}

func TestAppendAddsOnlyUnseenVideosAndPutsThemAtTheTail(t *testing.T) {
	store := NewSnapshotStore(time.Minute)
	id := store.Put("user1|Tech", ranked("a", "b"))

	// "b" is already in the snapshot: re-adding it would make the viewer see it
	// twice, which is the exact defect this whole mechanism exists to prevent.
	added := store.Append(id, ranked("b", "c", "d"))
	if added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}

	got, _ := store.Get(id)
	want := []string{"a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
	for i := range want {
		if got[i].VideoID != want[i] {
			t.Fatalf("snapshot = %v, want %v", got, want)
		}
	}
}

// A snapshot has to age from when it was made, not from when it was last read.
//
// Get used to refresh the expiry on every access, which made the thirty-minute
// TTL a sliding window: someone scrolling steadily never re-ranked, so nothing
// that arrived after they opened the page could ever reach the top of it.
func TestASnapshotExpiresEvenWhileItIsBeingRead(t *testing.T) {
	store := NewSnapshotStore(20 * time.Millisecond)
	id := store.Put("user1|Tech", ranked("a"))

	for i := 0; i < 8; i++ {
		time.Sleep(5 * time.Millisecond)
		store.Get(id)
	}

	if _, ok := store.Get(id); ok {
		t.Fatal("a snapshot read continuously never expired; the feed would be " +
			"frozen for as long as the viewer keeps scrolling")
	}
}

// What makes session intent visible. The feed reacts to the video that just
// finished only if the ordering it was frozen into is thrown away first.
func TestWatchingSomethingDropsThatViewersSnapshots(t *testing.T) {
	store := NewSnapshotStore(time.Minute)
	mine := store.Put("user1|", ranked("a", "b"))
	mineByTopic := store.Put("user1|Tech", ranked("a", "b"))
	theirs := store.Put("user2|", ranked("a", "b"))

	store.InvalidateUser("user1")

	if _, ok := store.Get(mine); ok {
		t.Fatal("the viewer's own snapshot survived their watching something")
	}
	if _, ok := store.Get(mineByTopic); ok {
		t.Fatal("a topic-filtered snapshot of the same viewer survived")
	}
	if _, ok := store.Get(theirs); !ok {
		t.Fatal("another household member's feed was reordered under them")
	}
}

// user1 must not take user10's snapshots with it. The key is a prefix, and a
// prefix match without the separator is the classic way to get this wrong.
func TestInvalidatingOneViewerDoesNotMatchASimilarlyNamedOne(t *testing.T) {
	store := NewSnapshotStore(time.Minute)
	other := store.Put("user10|", ranked("a"))

	store.InvalidateUser("user1")

	if _, ok := store.Get(other); !ok {
		t.Fatal("invalidating user1 also dropped user10's snapshot")
	}
}

func TestExpiredSnapshotIsReportedMissing(t *testing.T) {
	store := NewSnapshotStore(time.Millisecond)
	id := store.Put("user1|Tech", ranked("a"))
	time.Sleep(5 * time.Millisecond)

	if _, ok := store.Get(id); ok {
		t.Fatal("expired snapshot was still served")
	}
}
