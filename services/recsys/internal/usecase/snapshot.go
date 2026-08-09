package usecase

import (
	"strconv"
	"strings"
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
	ranked []domain.RankedVideo
	seen   map[string]struct{}
	// When the ordering was frozen. Expiry is measured from here and nowhere
	// else: reading a snapshot used to push its deadline back, which turned the
	// TTL into a sliding window and meant a viewer who kept scrolling never
	// re-ranked at all. The ordering they were given on opening the app was the
	// ordering they had until they stopped.
	created time.Time
}

func (e *snapshotEntry) expired(ttl time.Duration) bool {
	return time.Since(e.created) > ttl
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
		created: time.Now(),
	}
	return id
}

func (s *SnapshotStore) Get(id string) ([]domain.RankedVideo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[id]
	if !ok || entry.expired(s.ttl) {
		return nil, false
	}
	return entry.ranked, true
}

// InvalidateUser drops every frozen ordering belonging to one viewer.
//
// Called when they watch something. The snapshot exists so that paging through a
// feed does not repeat videos, and that is worth keeping — but it also means the
// ordering cannot respond to anything, and the one moment a feed most obviously
// should respond is the moment somebody finishes a video and comes back. Append
// is no answer: new material goes to the tail by design, which is exactly where
// a video matching what the viewer just watched must not go.
//
// The separator is part of the match. Snapshot ids are keyed "<user>|<topic>#n",
// so a bare prefix test would let user1 invalidate user10.
func (s *SnapshotStore) InvalidateUser(userID string) {
	if userID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	prefix := userID + "|"
	for id := range s.entries {
		if strings.HasPrefix(id, prefix) {
			delete(s.entries, id)
		}
	}
}

// Append adds videos the snapshot has not served yet, at the tail. New material
// arriving mid-scroll must go behind what the viewer has already passed, never
// into the middle of it.
func (s *SnapshotStore) Append(id string, extra []domain.RankedVideo) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[id]
	if !ok || entry.expired(s.ttl) {
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
	return added
}

func (s *SnapshotStore) evictExpiredLocked() {
	for id, entry := range s.entries {
		if entry.expired(s.ttl) {
			delete(s.entries, id)
		}
	}
}
