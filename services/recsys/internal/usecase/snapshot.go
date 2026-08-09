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
	// Whose ordering this is: "<user>|<topic>". Kept so that a client asking for
	// a first page can be given the session it is already reading rather than a
	// new one — see Latest.
	key string
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
		key:     key,
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

// Latest returns the newest live ordering for a key, if there is one.
//
// This is what keeps a scroll in one piece. A first-page request carries no
// token, and building a new ordering for every one of them looks harmless until
// you remember what a client actually does: an infinite query refetches *all* of
// its pages, replaying each stored page parameter — page one with nothing, page
// two with the token it already had. Page one would come back from a brand new
// ordering while page two was still reading the old one, and the two spliced
// together repeat whatever they have in common. Measured on this library: four
// duplicates in the first forty-eight videos.
//
// Reusing the live session makes a refetch idempotent. A genuinely new session
// still gets a new ordering — after the TTL, or after InvalidateUser drops it
// because the viewer watched something.
//
// Ids sort by their trailing counter, but only within a key; comparing creation
// times is what stays true regardless.
func (s *SnapshotStore) Latest(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		bestID string
		best   time.Time
	)
	for id, entry := range s.entries {
		if entry.key != key || entry.expired(s.ttl) {
			continue
		}
		if bestID == "" || entry.created.After(best) {
			bestID, best = id, entry.created
		}
	}
	return bestID, bestID != ""
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
