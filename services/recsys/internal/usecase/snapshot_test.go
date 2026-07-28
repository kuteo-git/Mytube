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

func TestExpiredSnapshotIsReportedMissing(t *testing.T) {
	store := NewSnapshotStore(time.Millisecond)
	id := store.Put("user1|Tech", ranked("a"))
	time.Sleep(5 * time.Millisecond)

	if _, ok := store.Get(id); ok {
		t.Fatal("expired snapshot was still served")
	}
}
