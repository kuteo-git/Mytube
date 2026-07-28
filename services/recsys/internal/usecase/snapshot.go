package usecase

import (
	"strconv"
	"sync"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// SnapshotStore freezes one feed ordering so paging through it is stable.
//
// Ranking is recomputed from scratch on every request, and recording an
// impression lowers the score of everything just shown. Slicing a freshly
// ranked list by offset therefore returns videos the viewer has already
// scrolled past — the list moved underneath the cursor. Freezing the order once
// and reading slices out of it is what makes "page 2" mean the same thing it
// meant when page 1 was served.
//
// Kept in memory deliberately. Losing a snapshot to a restart means the feed
// re-ranks, and the worst a viewer sees is a different order after scrolling
// again; that is not worth a table.
type SnapshotStore struct {
	ttl time.Duration

	mu      sync.Mutex
	nextID  int64
	entries map[string]*snapshotEntry
}

type snapshotEntry struct {
	ranked  []domain.RankedVideo
	seen    map[string]struct{}
	touched time.Time
}

func NewSnapshotStore(ttl time.Duration) *SnapshotStore {
	return &SnapshotStore{ttl: ttl, entries: map[string]*snapshotEntry{}}
}

// Put stores an ordering and returns its id. The key is carried only so that
// eviction of stale entries can be reasoned about; lookups go by id.
func (s *SnapshotStore) Put(key string, rankedVideos []domain.RankedVideo) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.evictExpiredLocked()
	s.nextID++
	id := key + "#" + strconv.FormatInt(s.nextID, 10)

	seen := make(map[string]struct{}, len(rankedVideos))
	for _, r := range rankedVideos {
		seen[r.VideoID] = struct{}{}
	}

	s.entries[id] = &snapshotEntry{
		ranked:  append([]domain.RankedVideo(nil), rankedVideos...),
		seen:    seen,
		touched: time.Now(),
	}
	return id
}

func (s *SnapshotStore) Get(id string) ([]domain.RankedVideo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[id]
	if !ok || time.Since(entry.touched) > s.ttl {
		return nil, false
	}
	entry.touched = time.Now()
	return entry.ranked, true
}

// Append adds videos the snapshot has not served yet, at the tail. New material
// arriving mid-scroll must go behind what the viewer has already passed, never
// into the middle of it.
func (s *SnapshotStore) Append(id string, extra []domain.RankedVideo) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[id]
	if !ok || time.Since(entry.touched) > s.ttl {
		return 0
	}

	added := 0
	for _, r := range extra {
		if _, dup := entry.seen[r.VideoID]; dup {
			continue
		}
		entry.seen[r.VideoID] = struct{}{}
		entry.ranked = append(entry.ranked, r)
		added++
	}
	entry.touched = time.Now()
	return added
}

func (s *SnapshotStore) evictExpiredLocked() {
	for id, entry := range s.entries {
		if time.Since(entry.touched) > s.ttl {
			delete(s.entries, id)
		}
	}
}
