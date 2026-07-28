# Infinite Feed & Eviction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the feed scroll without end and without repeating itself, by pulling new material in as the library runs low — and stop that from filling a 34 GiB disk by finally running the eviction sweep the schema was designed for.

**Architecture:** Three pieces that must ship together.

1. **Stable paging.** Today `sortAndPage` re-ranks the whole library on every page and slices by offset, while `recordImpressions` changes those very scores between pages. That is the mechanism producing duplicates right now, before any new source is added. Replaced by a per-session snapshot: the first request freezes an order, later pages read from the frozen list, and newly-arrived videos are appended to its tail.
2. **Backfill.** The gateway — the composition layer, and the only place allowed to orchestrate across services — notices the feed running low and asks ingest to expand the library. Ingest tries three sources in order of decreasing trust: deeper into the channels already in `topics.yaml`, then InnerTube related videos, then upstream search.
3. **Eviction.** A catalog-side sweep deleting media files of least-recently-accessed unpinned videos when usage passes 20 GiB, down to 16 GiB, keeping metadata, thumbnails and history.

**Why together:** items 1 and 2 raise the rate at which bytes land on disk; item 3 is the only thing removing them. Shipping 1 and 2 without 3 hands over a system that fills its own disk.

**Tech Stack:** Go (ConnectRPC, `buf` codegen, pgx), React 19 + TanStack Query v5.

## Global Constraints

- **All source code, identifiers, comments, commit messages and in-app UI copy MUST be in English.** (CLAUDE.md §4b)
- **No service queries another service's database.** Boundaries are enforced by DB permissions. Ingest never reads catalog's tables; it calls the service. (CLAUDE.md §3 rule 1)
- Clean architecture = **dependency direction**. `domain` imports no DB, HTTP or framework. (CLAUDE.md §3 rule 2)
- Proto changes go through `make proto` (`buf generate`); generated code is committed.
- **Eviction thresholds, fixed by the charter:** sweep triggers above **20 GiB**, deletes down to **16 GiB**, never touches `pinned` videos, and preserves metadata + thumbnail + history so the UI can offer a one-click re-ingest.
- **InnerTube is an undocumented internal API with no contract.** It must be a strictly optional layer: when it fails, the feed still refills from the deepening layer. Never let it fail a request.
- Verification: `go test ./...` and `make check`.

---

### Task 1: Freeze the feed order per session

**Files:**
- Create: `services/recsys/internal/usecase/snapshot.go`
- Create: `services/recsys/internal/usecase/snapshot_test.go`
- Modify: `services/recsys/internal/usecase/ranker.go:74-131` (`GetFeed`)
- Modify: `services/recsys/internal/adapter/rpc/server.go:54-69` (`GetFeed`)
- Modify: `services/recsys/internal/adapter/rpc/token.go`

**Interfaces:**
- Consumes: `domain.RankedVideo{VideoID string; Score float64; Reason Reason}`.
- Produces:
  - `type SnapshotStore struct{ ... }`; `NewSnapshotStore(ttl time.Duration) *SnapshotStore`
  - `(*SnapshotStore).Put(key string, ranked []domain.RankedVideo) string` — returns a new snapshot id
  - `(*SnapshotStore).Get(id string) ([]domain.RankedVideo, bool)`
  - `(*SnapshotStore).Append(id string, extra []domain.RankedVideo) int` — appends only ids not already present, returns how many were added
  - `(*Ranker).GetFeedPage(ctx, userID, topic, snapshotID string, pageSize int32, offset int32) (page []domain.RankedVideo, newSnapshotID string, total int, err error)`

- [ ] **Step 1: Write the failing test**

Create `services/recsys/internal/usecase/snapshot_test.go`:

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./services/recsys/internal/usecase/ -run TestSnapshot -v`
Expected: FAIL to build — `undefined: NewSnapshotStore`.

- [ ] **Step 3: Implement the snapshot store**

Create `services/recsys/internal/usecase/snapshot.go`:

```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./services/recsys/internal/usecase/ -run TestSnapshot -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add services/recsys/internal/usecase/snapshot.go services/recsys/internal/usecase/snapshot_test.go
git commit -m "Freeze a feed ordering so paging stops repeating itself"
```

---

### Task 2: Serve feed pages out of the snapshot

**Files:**
- Modify: `services/recsys/internal/usecase/ranker.go`
- Create: `services/recsys/internal/usecase/ranker_page_test.go`
- Modify: `services/recsys/internal/adapter/rpc/server.go`
- Modify: `services/recsys/internal/adapter/rpc/token.go`
- Modify: `services/recsys/cmd/recsys/main.go` (construct the store)

**Interfaces:**
- Consumes: `SnapshotStore` from Task 1.
- Produces: `(*Ranker).GetFeedPage(ctx context.Context, userID, topic, snapshotID string, pageSize, offset int32) (FeedPage, error)` where:

```go
type FeedPage struct {
	Videos     []domain.RankedVideo
	SnapshotID string
	Remaining  int // entries left in the snapshot after this page
}
```

- [ ] **Step 1: Write the failing test**

Create `services/recsys/internal/usecase/ranker_page_test.go`:

```go
package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

type stubFeatures struct{ features []domain.VideoFeatures }

func (s stubFeatures) ListVideoFeatures(context.Context) ([]domain.VideoFeatures, error) {
	return s.features, nil
}

type stubStore struct{ profile domain.UserProfile }

func (stubStore) AppendSignal(context.Context, domain.Signal) error            { return nil }
func (stubStore) RecordImpressions(context.Context, string, []string) error    { return nil }
func (s stubStore) BuildProfile(context.Context, string, time.Duration) (domain.UserProfile, error) {
	return s.profile, nil
}

func emptyProfile() domain.UserProfile {
	return domain.UserProfile{
		WatchedFraction:   map[string]float32{},
		Liked:             map[string]bool{},
		Disliked:          map[string]bool{},
		Subscribed:        map[string]bool{},
		RecentImpressions: map[string]bool{},
	}
}

func features(n int) []domain.VideoFeatures {
	out := make([]domain.VideoFeatures, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, domain.VideoFeatures{
			VideoID:   string(rune('a'+i/26)) + string(rune('a'+i%26)),
			ChannelID: "chan1",
			AddedAt:   time.Now(),
		})
	}
	return out
}

// The defect this guards: recording impressions lowers the score of everything
// just served, so a re-ranked page 2 pulls up videos that were on page 1.
func TestPagingNeverRepeatsAVideo(t *testing.T) {
	ranker := NewRanker(stubStore{profile: emptyProfile()}, stubFeatures{features: features(50)})
	ranker.snapshots = NewSnapshotStore(time.Minute)

	ctx := context.Background()
	first, err := ranker.GetFeedPage(ctx, "user1", "", "", 20, 0)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(first.Videos) != 20 {
		t.Fatalf("page 1 returned %d, want 20", len(first.Videos))
	}

	// Simulate the impression penalty landing between pages.
	profile := emptyProfile()
	for _, v := range first.Videos {
		profile.RecentImpressions[v.VideoID] = true
	}
	ranker.store = stubStore{profile: profile}

	second, err := ranker.GetFeedPage(ctx, "user1", "", first.SnapshotID, 20, 20)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}

	seen := map[string]bool{}
	for _, v := range first.Videos {
		seen[v.VideoID] = true
	}
	for _, v := range second.Videos {
		if seen[v.VideoID] {
			t.Fatalf("video %s appeared on both pages", v.VideoID)
		}
	}
}

func TestRemainingReportsHowMuchFeedIsLeft(t *testing.T) {
	ranker := NewRanker(stubStore{profile: emptyProfile()}, stubFeatures{features: features(50)})
	ranker.snapshots = NewSnapshotStore(time.Minute)

	page, err := ranker.GetFeedPage(context.Background(), "user1", "", "", 20, 0)
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if page.Remaining != 30 {
		t.Fatalf("Remaining = %d, want 30", page.Remaining)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./services/recsys/internal/usecase/ -run "TestPaging|TestRemaining" -v`
Expected: FAIL to build — `ranker.snapshots undefined`, `ranker.GetFeedPage undefined`.

- [ ] **Step 3: Add the snapshot store to the ranker and implement paging**

In `services/recsys/internal/usecase/ranker.go`, change the `Ranker` struct and constructor:

```go
type Ranker struct {
	store     domain.SignalStore
	features  domain.FeatureSource
	snapshots *SnapshotStore
	now       func() time.Time
}

func NewRanker(store domain.SignalStore, features domain.FeatureSource) *Ranker {
	return &Ranker{
		store:     store,
		features:  features,
		snapshots: NewSnapshotStore(30 * time.Minute),
		now:       time.Now,
	}
}
```

Add above `GetFeed`:

```go
// FeedPage is one slice of a frozen feed ordering, plus what the caller needs
// to ask for the next one and to know how close the feed is to running out.
type FeedPage struct {
	Videos     []domain.RankedVideo
	SnapshotID string
	Remaining  int
}

// GetFeedPage serves from a frozen ordering rather than re-ranking per page.
//
// An empty snapshotID means "start a session": rank everything, freeze it, and
// hand back an id. A known snapshotID reads from that frozen list, after
// appending anything the library has gained since — new material belongs at the
// tail, behind what the viewer has already scrolled past.
//
// An unknown or expired snapshotID silently starts a new session. Thirty
// minutes into a scroll, re-ranking is a smaller surprise than an error page.
func (r *Ranker) GetFeedPage(ctx context.Context, userID, topic, snapshotID string, pageSize, offset int32) (FeedPage, error) {
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 24
	}

	fresh, err := r.rankAll(ctx, userID, topic)
	if err != nil {
		return FeedPage{}, err
	}

	ordering, ok := r.snapshots.Get(snapshotID)
	if !ok {
		snapshotID = r.snapshots.Put(userID+"|"+topic, fresh)
		ordering, _ = r.snapshots.Get(snapshotID)
		offset = 0
	} else if r.snapshots.Append(snapshotID, fresh) > 0 {
		ordering, _ = r.snapshots.Get(snapshotID)
	}

	start := int(offset)
	if start > len(ordering) {
		start = len(ordering)
	}
	end := start + int(pageSize)
	if end > len(ordering) {
		end = len(ordering)
	}

	return FeedPage{
		Videos:     ordering[start:end],
		SnapshotID: snapshotID,
		Remaining:  len(ordering) - end,
	}, nil
}
```

Then refactor the existing `GetFeed` body into `rankAll`, keeping the scoring identical and dropping only the paging: rename `func (r *Ranker) GetFeed(ctx context.Context, userID, topic string, pageSize, offset int32) ([]domain.RankedVideo, error)` to `func (r *Ranker) rankAll(ctx context.Context, userID, topic string) ([]domain.RankedVideo, error)`, delete the `pageSize`/`offset` parameters, and replace the final line `return sortAndPage(ranked, pageSize, offset), nil` with:

```go
	sortRanked(ranked)
	return ranked, nil
```

Replace `sortAndPage` (lines 263-285) with a sort-only helper, since `GetUpNext` still needs paging:

```go
// sortRanked orders by score, breaking ties on video id so that repeated
// requests with identical scores return a stable order rather than shuffling.
func sortRanked(rankedVideos []domain.RankedVideo) {
	sort.Slice(rankedVideos, func(i, j int) bool {
		if rankedVideos[i].Score != rankedVideos[j].Score {
			return rankedVideos[i].Score > rankedVideos[j].Score
		}
		return rankedVideos[i].VideoID < rankedVideos[j].VideoID
	})
}

func sortAndPage(rankedVideos []domain.RankedVideo, pageSize, offset int32) []domain.RankedVideo {
	sortRanked(rankedVideos)

	if pageSize <= 0 || pageSize > 100 {
		pageSize = 24
	}
	start := int(offset)
	if start > len(rankedVideos) {
		start = len(rankedVideos)
	}
	end := start + int(pageSize)
	if end > len(rankedVideos) {
		end = len(rankedVideos)
	}
	return rankedVideos[start:end]
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./services/recsys/internal/usecase/ -v`
Expected: PASS.

- [ ] **Step 5: Carry the snapshot id in the page token**

Read `services/recsys/internal/adapter/rpc/token.go` in full. It currently encodes an integer offset. Replace its contents:

```go
package rpc

import (
	"encoding/base64"
	"strconv"
	"strings"
)

// Page tokens are opaque to the client by contract, so their shape can change
// freely. They now carry two things: which frozen ordering to read, and how far
// into it the reader is.
func encodeToken(snapshotID string, offset int32) string {
	raw := snapshotID + "|" + strconv.Itoa(int(offset))
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeToken(token string) (snapshotID string, offset int32) {
	if token == "" {
		return "", 0
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", 0
	}
	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 {
		return "", 0
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n < 0 {
		return parts[0], 0
	}
	return parts[0], int32(n)
}
```

**Note:** if `token.go` currently exports `decodeToken(string) int32` used anywhere else, `go build` will tell you. `GetUpNext` does not page, so `GetFeed` should be the only caller.

- [ ] **Step 6: Update the RPC server**

In `services/recsys/internal/adapter/rpc/server.go`, replace `GetFeed`:

```go
func (s *Server) GetFeed(ctx context.Context, req *connect.Request[recsysv1.GetFeedRequest]) (*connect.Response[recsysv1.GetFeedResponse], error) {
	snapshotID, offset := decodeToken(req.Msg.GetPageToken())

	page, err := s.ranker.GetFeedPage(ctx, req.Msg.GetUserId(), req.Msg.GetCategory(),
		snapshotID, req.Msg.GetPageSize(), offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	next := ""
	if page.Remaining > 0 {
		next = encodeToken(page.SnapshotID, offset+int32(len(page.Videos)))
	}
	return connect.NewResponse(&recsysv1.GetFeedResponse{
		Videos:         toProto(page.Videos),
		NextPageToken:  next,
		RemainingCount: int32(page.Remaining),
	}), nil
}
```

- [ ] **Step 7: Add `remaining_count` to the proto**

In `proto/recsys/v1/recsys.proto`, add to `GetFeedResponse` (use the next free field number in that message):

```proto
  // How many ranked videos are left after this page. The gateway uses it to
  // decide when to go looking for more material, before the viewer hits the end.
  int32 remaining_count = 3;
```

Then run: `make proto`

- [ ] **Step 8: Verify everything builds and passes**

Run: `make check && go test ./...`
Expected: both succeed.

- [ ] **Step 9: Commit**

```bash
git add proto/recsys/v1/recsys.proto gen/ services/recsys/
git commit -m "Serve feed pages from a frozen ordering"
```

---

### Task 3: Reach InnerTube for related videos

`yt-dlp` does not expose related videos, and neither does the .NET `YoutubeExplode`. The Dart port and every JS client get them from the same place: `POST https://www.youtube.com/youtubei/v1/next`, whose response carries `contents.twoColumnWatchNextResults.secondaryResults`. That is one HTTP call, so this is an 80-line adapter rather than a dependency.

**Files:**
- Create: `services/ingest/internal/adapter/innertube/client.go`
- Create: `services/ingest/internal/adapter/innertube/client_test.go`

**Interfaces:**
- Produces: `innertube.Client` with `Related(ctx context.Context, videoID string) ([]domain.ExternalVideo, error)`; `innertube.New(httpClient *http.Client) *Client`.
- The returned `ExternalVideo` values carry `ID`, `Title`, `ChannelName`, `DurationSeconds`, `ThumbnailURL`, `SourceURL`. They carry **no** `Topics` — related videos are not assigned to a topic, exactly as `EnsureVideo` already refuses to (`ingest.go:55-57`).

- [ ] **Step 1: Write the failing test**

Create `services/ingest/internal/adapter/innertube/client_test.go`. It parses a trimmed-down but structurally real response, so the parser is tested without touching the network:

```go
package innertube

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const sampleResponse = `{
  "contents": {
    "twoColumnWatchNextResults": {
      "secondaryResults": {
        "secondaryResults": {
          "results": [
            {
              "compactVideoRenderer": {
                "videoId": "abc123",
                "title": { "simpleText": "A related video" },
                "longBylineText": { "runs": [ { "text": "Some Channel" } ] },
                "lengthText": { "simpleText": "12:34" },
                "thumbnail": { "thumbnails": [ { "url": "https://i.ytimg.com/vi/abc123/hq.jpg" } ] }
              }
            },
            { "continuationItemRenderer": { "trigger": "unused" } }
          ]
        }
      }
    }
  }
}`

func TestRelatedParsesCompactVideoRenderers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleResponse))
	}))
	defer server.Close()

	client := New(server.Client())
	client.endpoint = server.URL

	videos, err := client.Related(t.Context(), "seed1")
	if err != nil {
		t.Fatalf("Related: %v", err)
	}
	if len(videos) != 1 {
		t.Fatalf("got %d videos, want 1 (non-video renderers must be skipped)", len(videos))
	}

	v := videos[0]
	if v.ID != "abc123" {
		t.Errorf("ID = %q, want abc123", v.ID)
	}
	if v.Title != "A related video" {
		t.Errorf("Title = %q", v.Title)
	}
	if v.ChannelName != "Some Channel" {
		t.Errorf("ChannelName = %q", v.ChannelName)
	}
	if v.DurationSeconds != 754 {
		t.Errorf("DurationSeconds = %d, want 754", v.DurationSeconds)
	}
	if v.SourceURL != "https://www.youtube.com/watch?v=abc123" {
		t.Errorf("SourceURL = %q", v.SourceURL)
	}
	if len(v.Topics) != 0 {
		t.Errorf("related videos must not be assigned a topic, got %v", v.Topics)
	}
}

func TestRelatedReturnsNothingRatherThanFailingOnUnexpectedShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"contents":{}}`))
	}))
	defer server.Close()

	client := New(server.Client())
	client.endpoint = server.URL

	videos, err := client.Related(t.Context(), "seed1")
	if err != nil {
		t.Fatalf("a shape change must not be an error, got %v", err)
	}
	if len(videos) != 0 {
		t.Fatalf("got %d videos, want 0", len(videos))
	}
}

func TestParseDuration(t *testing.T) {
	cases := map[string]int32{
		"0:45":    45,
		"12:34":   754,
		"1:02:03": 3723,
		"":        0,
		"LIVE":    0,
	}
	for input, want := range cases {
		if got := parseDuration(input); got != want {
			t.Errorf("parseDuration(%q) = %d, want %d", input, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./services/ingest/internal/adapter/innertube/ -v`
Expected: FAIL to build — package does not exist.

- [ ] **Step 3: Implement the client**

Create `services/ingest/internal/adapter/innertube/client.go`:

```go
// Package innertube reads YouTube's internal watch-next API.
//
// This exists because nothing else offers related videos: yt-dlp does not
// expose them and the .NET YoutubeExplode has no such call. Every library that
// does offer them reads the same endpoint this does.
//
// It is an undocumented API with no compatibility contract, so every function
// here is written to degrade to "no results" rather than to an error. A caller
// must be able to treat a total failure as an empty list — the feed has other
// ways to refill, and losing variety is a far better outcome than a broken page.
package innertube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

const defaultEndpoint = "https://www.youtube.com/youtubei/v1/next"

// The web client identity YouTube's own page sends. Version drift here is the
// most likely cause of a sudden empty result, and is the first thing to check.
const (
	clientName    = "WEB"
	clientVersion = "2.20240101.00.00"
)

type Client struct {
	http     *http.Client
	endpoint string
}

func New(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{http: httpClient, endpoint: defaultEndpoint}
}

type nextRequest struct {
	VideoID string      `json:"videoId"`
	Context nextContext `json:"context"`
}

type nextContext struct {
	Client nextClient `json:"client"`
}

type nextClient struct {
	ClientName    string `json:"clientName"`
	ClientVersion string `json:"clientVersion"`
	Hl            string `json:"hl"`
	Gl            string `json:"gl"`
}

// Only the fields that are actually read are declared. Everything else in the
// response — and there is a great deal of it — is discarded by the decoder.
type nextResponse struct {
	Contents struct {
		TwoColumnWatchNextResults struct {
			SecondaryResults struct {
				SecondaryResults struct {
					Results []struct {
						CompactVideoRenderer *compactVideoRenderer `json:"compactVideoRenderer"`
					} `json:"results"`
				} `json:"secondaryResults"`
			} `json:"secondaryResults"`
		} `json:"twoColumnWatchNextResults"`
	} `json:"contents"`
}

type compactVideoRenderer struct {
	VideoID string `json:"videoId"`
	Title   struct {
		SimpleText string `json:"simpleText"`
	} `json:"title"`
	LongBylineText struct {
		Runs []struct {
			Text string `json:"text"`
		} `json:"runs"`
	} `json:"longBylineText"`
	LengthText struct {
		SimpleText string `json:"simpleText"`
	} `json:"lengthText"`
	Thumbnail struct {
		Thumbnails []struct {
			URL string `json:"url"`
		} `json:"thumbnails"`
	} `json:"thumbnail"`
}

// Related returns the videos YouTube would show beside the given one.
//
// Anonymous requests get a thinner list than a signed-in browser would, because
// this panel is heavily personalised. That is accepted: the alternative is
// storing YouTube credentials, which this project does not do.
func (c *Client) Related(ctx context.Context, videoID string) ([]domain.ExternalVideo, error) {
	body, err := json.Marshal(nextRequest{
		VideoID: videoID,
		Context: nextContext{Client: nextClient{
			ClientName:    clientName,
			ClientVersion: clientVersion,
			Hl:            "en",
			Gl:            "US",
		}},
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Youtube-Client-Name", "1")
	req.Header.Set("X-Youtube-Client-Version", clientVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("innertube next: status %d", resp.StatusCode)
	}

	var decoded nextResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		// A shape change is not an error the caller can act on, and treating it
		// as one would take the feed down with it.
		return nil, nil
	}

	results := decoded.Contents.TwoColumnWatchNextResults.SecondaryResults.SecondaryResults.Results
	out := make([]domain.ExternalVideo, 0, len(results))
	for _, entry := range results {
		r := entry.CompactVideoRenderer
		if r == nil || r.VideoID == "" {
			continue // continuation markers and ad slots live in this list too
		}

		channelName := ""
		if len(r.LongBylineText.Runs) > 0 {
			channelName = r.LongBylineText.Runs[0].Text
		}
		thumbnail := ""
		if n := len(r.Thumbnail.Thumbnails); n > 0 {
			thumbnail = r.Thumbnail.Thumbnails[n-1].URL // last is largest
		}

		out = append(out, domain.ExternalVideo{
			ID:              r.VideoID,
			Title:           r.Title.SimpleText,
			ChannelName:     channelName,
			DurationSeconds: parseDuration(r.LengthText.SimpleText),
			ThumbnailURL:    thumbnail,
			SourceURL:       "https://www.youtube.com/watch?v=" + r.VideoID,
			// Deliberately no Topics. Topics say "the curator chose this
			// source"; a related video was chosen by YouTube.
		})
	}
	return out, nil
}

// parseDuration reads "12:34" and "1:02:03". Live streams have no length and
// give an empty or non-numeric string, which becomes zero.
func parseDuration(text string) int32 {
	parts := strings.Split(strings.TrimSpace(text), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	total := 0
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return 0
		}
		total = total*60 + n
	}
	return int32(total)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./services/ingest/internal/adapter/innertube/ -v`
Expected: PASS (all three).

- [ ] **Step 5: Confirm it works against the real endpoint, once**

Write a throwaway check and delete it afterwards — this is a manual confirmation, not a committed test, because a committed test that calls YouTube would fail in any offline environment:

```bash
cat > /tmp/innertube_live_test.go <<'EOF'
package innertube

import "testing"

func TestLive(t *testing.T) {
	videos, err := New(nil).Related(t.Context(), "dQw4w9WgXcQ")
	t.Logf("err=%v count=%d", err, len(videos))
	for i, v := range videos {
		if i >= 3 {
			break
		}
		t.Logf("%s | %s | %s", v.ID, v.ChannelName, v.Title)
	}
}
EOF
cp /tmp/innertube_live_test.go services/ingest/internal/adapter/innertube/
go test ./services/ingest/internal/adapter/innertube/ -run TestLive -v
rm services/ingest/internal/adapter/innertube/innertube_live_test.go
```

Expected: a non-zero count with plausible titles. **If the count is zero**, the client version constant is the first suspect — check what `youtube.com` currently sends as `X-Youtube-Client-Version` in DevTools and update `clientVersion`. Record the outcome either way; a zero here means the related layer contributes nothing and the feed will lean entirely on deepening and search, which is a working but less varied outcome.

- [ ] **Step 6: Commit**

```bash
git add services/ingest/internal/adapter/innertube/
git commit -m "Read related videos from YouTube's watch-next endpoint"
```

---

### Task 4: Expand the library on demand

**Files:**
- Modify: `proto/ingest/v1/ingest.proto` (add `ExpandLibrary`)
- Create: `services/ingest/internal/usecase/expand.go`
- Create: `services/ingest/internal/usecase/expand_test.go`
- Modify: `services/ingest/internal/domain/ingest.go` (`Downloader.ListPlaylist` gains an offset; new `RelatedSource` port)
- Modify: `services/ingest/internal/adapter/ytdlp/downloader.go` (`ListPlaylist` offset via `--playlist-start`)
- Modify: `services/ingest/internal/adapter/rpc/server.go`
- Modify: `services/ingest/cmd/ingest/main.go`
- Create: `services/ingest/migrations/` — a migration adding `source_cursors`

**Interfaces:**
- Produces:
  - `domain.RelatedSource` port: `Related(ctx context.Context, videoID string) ([]ExternalVideo, error)` — implemented by `innertube.Client` from Task 3.
  - `domain.Downloader.ListPlaylist(ctx context.Context, url string, offset, limit int32) (title string, videos []ExternalVideo, err error)` — **signature change**, existing callers in `scanner.go` must pass `0` for offset.
  - `domain.CursorStore` port: `NextOffset(ctx, sourceURL string) (int32, error)`; `AdvanceOffset(ctx, sourceURL string, by int32) error`
  - `(*Expander).Expand(ctx context.Context, topic string, seedVideoIDs []string) (added int, err error)`
  - RPC `ExpandLibrary(ExpandLibraryRequest{topic, seed_video_ids}) returns (ExpandLibraryResponse{videos_added})`

- [ ] **Step 1: Write the failing test**

Create `services/ingest/internal/usecase/expand_test.go`:

```go
package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

type recordingLibrary struct {
	added []string
	known map[string]bool
}

func (r *recordingLibrary) FindBySourceURL(_ context.Context, url string) (string, bool, error) {
	return "", r.known[url], nil
}
func (r *recordingLibrary) UpsertChannel(context.Context, domain.ExternalVideo) error { return nil }
func (r *recordingLibrary) UpsertVideo(_ context.Context, v domain.ExternalVideo, _ string) error {
	r.added = append(r.added, v.ID)
	return nil
}
func (r *recordingLibrary) SetMediaState(context.Context, string, string, string, int64, []domain.SubtitleTrack) error {
	return nil
}
func (r *recordingLibrary) SourceURLFor(context.Context, string) (string, error) { return "", nil }

type stubCursors struct{ offsets map[string]int32 }

func (s *stubCursors) NextOffset(_ context.Context, url string) (int32, error) {
	return s.offsets[url], nil
}
func (s *stubCursors) AdvanceOffset(_ context.Context, url string, by int32) error {
	s.offsets[url] += by
	return nil
}

type stubTopics struct{}

func (stubTopics) Load(context.Context) (domain.TopicConfig, error) {
	return domain.TopicConfig{
		Topics: []domain.Topic{{
			Name:    "Tech",
			Sources: []string{"https://youtube.test/@a/videos"},
		}},
		PerSourceLimit: 2,
	}, nil
}

type deepenDownloader struct {
	offsetsAsked []int32
}

func (d *deepenDownloader) Search(context.Context, string, int32) ([]domain.ExternalVideo, error) {
	return []domain.ExternalVideo{{ID: "search1", SourceURL: "https://youtube.test/watch?v=search1"}}, nil
}
func (d *deepenDownloader) Preview(context.Context, string) (domain.ExternalVideo, error) {
	return domain.ExternalVideo{}, nil
}
func (d *deepenDownloader) ListPlaylist(_ context.Context, _ string, offset, _ int32) (string, []domain.ExternalVideo, error) {
	d.offsetsAsked = append(d.offsetsAsked, offset)
	return "A", []domain.ExternalVideo{
		{ID: "deep1", SourceURL: "https://youtube.test/watch?v=deep1"},
		{ID: "deep2", SourceURL: "https://youtube.test/watch?v=deep2"},
	}, nil
}
func (d *deepenDownloader) ResolveStream(context.Context, string) (domain.StreamLocation, error) {
	return domain.StreamLocation{}, nil
}
func (d *deepenDownloader) FetchSubtitles(context.Context, string, string, int32) []domain.SubtitleTrack {
	return nil
}
func (d *deepenDownloader) Download(context.Context, string, string, int32, func(domain.Progress)) (domain.DownloadResult, error) {
	return domain.DownloadResult{}, nil
}

type failingRelated struct{}

func (failingRelated) Related(context.Context, string) ([]domain.ExternalVideo, error) {
	return nil, errors.New("innertube is down")
}

func newExpander(d domain.Downloader, related domain.RelatedSource, lib domain.Library, cursors domain.CursorStore) *Expander {
	return NewExpander(d, related, lib, stubTopics{}, cursors,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// Deepening comes first because it draws only on sources the user curated.
func TestExpandDeepensCuratedSourcesBeforeAnythingElse(t *testing.T) {
	downloader := &deepenDownloader{}
	library := &recordingLibrary{known: map[string]bool{}}
	cursors := &stubCursors{offsets: map[string]int32{"https://youtube.test/@a/videos": 40}}

	expander := newExpander(downloader, failingRelated{}, library, cursors)

	added, err := expander.Expand(context.Background(), "Tech", nil)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}
	if len(downloader.offsetsAsked) != 1 || downloader.offsetsAsked[0] != 40 {
		t.Fatalf("offsets asked = %v, want [40] — the cursor must advance past what was already scanned", downloader.offsetsAsked)
	}
	if cursors.offsets["https://youtube.test/@a/videos"] != 42 {
		t.Errorf("cursor = %d, want 42", cursors.offsets["https://youtube.test/@a/videos"])
	}
}

// The whole point of layering: InnerTube can vanish without taking the feed
// with it.
func TestExpandSurvivesRelatedSourceFailure(t *testing.T) {
	library := &recordingLibrary{known: map[string]bool{}}
	expander := newExpander(&deepenDownloader{}, failingRelated{}, library,
		&stubCursors{offsets: map[string]int32{}})

	if _, err := expander.Expand(context.Background(), "Tech", []string{"seed1"}); err != nil {
		t.Fatalf("a failing related source must not fail the expansion: %v", err)
	}
}

// Videos already in the library must not be written again.
func TestExpandSkipsVideosAlreadyPresent(t *testing.T) {
	library := &recordingLibrary{known: map[string]bool{
		"https://youtube.test/watch?v=deep1": true,
	}}
	expander := newExpander(&deepenDownloader{}, failingRelated{}, library,
		&stubCursors{offsets: map[string]int32{}})

	added, err := expander.Expand(context.Background(), "Tech", nil)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	if len(library.added) != 1 || library.added[0] != "deep2" {
		t.Fatalf("added %v, want [deep2]", library.added)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./services/ingest/internal/usecase/ -run TestExpand -v`
Expected: FAIL to build — `undefined: Expander`, `undefined: domain.RelatedSource`, `undefined: domain.CursorStore`, and `ListPlaylist` arity mismatch.

- [ ] **Step 3: Add the ports**

In `services/ingest/internal/domain/ingest.go`, change the `ListPlaylist` line of the `Downloader` interface to:

```go
	// offset skips entries already scanned, which is how the library is
	// deepened past the most recent few dozen uploads.
	ListPlaylist(ctx context.Context, url string, offset, limit int32) (string, []ExternalVideo, error)
```

and add, after the `Library` interface:

```go
// RelatedSource is the port over YouTube's watch-next panel. It is deliberately
// separate from Downloader: it speaks to a different, undocumented API, and
// callers are required to treat its failure as "no results" rather than as an
// error worth surfacing.
type RelatedSource interface {
	Related(ctx context.Context, videoID string) ([]ExternalVideo, error)
}

// CursorStore remembers how far into each source the library has been filled,
// so deepening resumes rather than re-reading the same first page forever.
type CursorStore interface {
	NextOffset(ctx context.Context, sourceURL string) (int32, error)
	AdvanceOffset(ctx context.Context, sourceURL string, by int32) error
}
```

- [ ] **Step 4: Implement the expander**

Create `services/ingest/internal/usecase/expand.go`:

```go
package usecase

import (
	"context"
	"log/slog"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// How many videos one expansion pass aims to add. Small enough that a pass is
// quick, large enough to stay ahead of someone scrolling.
const expandTarget = 40

// Expander brings new material into the library when the feed runs low.
//
// Three layers, tried in order of decreasing trust:
//
//  1. Deeper into the sources in topics.yaml. These are channels the user chose;
//     a channel with a thousand uploads has been read forty deep. Nothing here
//     can fail in a way that surprises anyone.
//  2. Related videos from InnerTube. Genuinely new channels, at the cost of an
//     undocumented API — so its failure is logged and stepped over, never
//     returned.
//  3. Upstream search on the topic name. The last resort, because search results
//     are the least curated material available.
//
// The ordering is the design. If layer 2 breaks permanently, the feed still
// refills from layer 1; it is only less varied.
type Expander struct {
	downloader domain.Downloader
	related    domain.RelatedSource
	library    domain.Library
	topics     domain.TopicSource
	cursors    domain.CursorStore
	logger     *slog.Logger
}

func NewExpander(
	downloader domain.Downloader,
	related domain.RelatedSource,
	library domain.Library,
	topics domain.TopicSource,
	cursors domain.CursorStore,
	logger *slog.Logger,
) *Expander {
	return &Expander{
		downloader: downloader,
		related:    related,
		library:    library,
		topics:     topics,
		cursors:    cursors,
		logger:     logger,
	}
}

func (e *Expander) Expand(ctx context.Context, topic string, seedVideoIDs []string) (int, error) {
	added := 0

	added += e.deepen(ctx, topic)
	if added >= expandTarget {
		return added, nil
	}

	added += e.fromRelated(ctx, seedVideoIDs)
	if added >= expandTarget {
		return added, nil
	}

	added += e.fromSearch(ctx, topic)
	return added, nil
}

// deepen reads further into the curated sources for this topic, resuming from
// the stored cursor so each pass sees material the last one did not.
func (e *Expander) deepen(ctx context.Context, topic string) int {
	config, err := e.topics.Load(ctx)
	if err != nil {
		e.logger.Warn("load topics", "error", err)
		return 0
	}

	added := 0
	for _, t := range config.Topics {
		if topic != "" && !strings.EqualFold(t.Name, topic) {
			continue
		}
		for _, source := range t.Sources {
			offset, err := e.cursors.NextOffset(ctx, source)
			if err != nil {
				e.logger.Warn("read source cursor", "source", source, "error", err)
				continue
			}

			_, videos, err := e.downloader.ListPlaylist(ctx, source, offset, config.PerSourceLimit)
			if err != nil {
				e.logger.Warn("deepen source", "source", source, "error", err)
				continue
			}
			if len(videos) == 0 {
				continue // source exhausted
			}

			for i := range videos {
				videos[i].Topics = []string{t.Name}
			}
			added += e.store(ctx, videos)

			if err := e.cursors.AdvanceOffset(ctx, source, int32(len(videos))); err != nil {
				e.logger.Warn("advance source cursor", "source", source, "error", err)
			}
			if added >= expandTarget {
				return added
			}
		}
	}
	return added
}

// fromRelated asks YouTube what sits beside videos the viewer has watched.
// Failure is expected occasionally and is never returned upward.
func (e *Expander) fromRelated(ctx context.Context, seedVideoIDs []string) int {
	added := 0
	for _, seed := range seedVideoIDs {
		videos, err := e.related.Related(ctx, seed)
		if err != nil {
			e.logger.Warn("related lookup", "seed", seed, "error", err)
			continue
		}
		added += e.store(ctx, videos)
		if added >= expandTarget {
			return added
		}
	}
	return added
}

func (e *Expander) fromSearch(ctx context.Context, topic string) int {
	if topic == "" {
		return 0
	}
	videos, err := e.downloader.Search(ctx, topic, expandTarget)
	if err != nil {
		e.logger.Warn("expand by search", "topic", topic, "error", err)
		return 0
	}
	return e.store(ctx, videos)
}

// store writes metadata only. Nothing is downloaded here: a video becomes a row
// the feed can rank, and bytes are fetched later, if and when someone presses
// play. That is what keeps an expansion cheap enough to run mid-scroll.
func (e *Expander) store(ctx context.Context, videos []domain.ExternalVideo) int {
	added := 0
	for _, v := range videos {
		if v.ID == "" || v.SourceURL == "" {
			continue
		}
		if _, found, err := e.library.FindBySourceURL(ctx, v.SourceURL); err == nil && found {
			continue
		}
		if err := e.library.UpsertChannel(ctx, v); err != nil {
			e.logger.Warn("upsert channel", "video", v.ID, "error", err)
			continue
		}
		if err := e.library.UpsertVideo(ctx, v, "ABSENT"); err != nil {
			e.logger.Warn("upsert video", "video", v.ID, "error", err)
			continue
		}
		added++
	}
	return added
}
```

Add `"strings"` to the import block.

**Note:** the `"ABSENT"` media state must match whatever `UpsertVideo` already accepts for a metadata-only row. Check `scanner.go:130+` for the string the scanner passes and use the same one — do not invent a new state.

- [ ] **Step 5: Update `ListPlaylist` in the adapter and the scanner**

In `services/ingest/internal/adapter/ytdlp/downloader.go`, add the offset parameter to `ListPlaylist` and translate it into `--playlist-start` (1-based) and `--playlist-end`:

```go
func (d *Downloader) ListPlaylist(ctx context.Context, url string, offset, limit int32) (string, []domain.ExternalVideo, error) {
```

and in the command construction, replace whatever currently caps the listing with:

```go
	// yt-dlp's playlist range is 1-based and inclusive at both ends.
	start := offset + 1
	end := offset + limit
	cmd = cmd.PlaylistStart(int(start)).PlaylistEnd(int(end))
```

(Read the existing function first; keep every other flag it sets. If `PlaylistStart`/`PlaylistEnd` are not the method names in this `go-ytdlp` version, find the equivalents — `go doc github.com/lrstanley/go-ytdlp | grep -i playlist`.)

In `services/ingest/internal/usecase/scanner.go`, update the single call site to pass `0` as offset.

- [ ] **Step 6: Add the cursor store**

Create the migration `services/ingest/migrations/0002_source_cursors.sql`:

```sql
-- How far into each source the library has been filled. Deepening resumes from
-- here rather than re-reading the newest forty uploads on every pass.
CREATE TABLE IF NOT EXISTS source_cursors (
  source_url  text        PRIMARY KEY,
  next_offset integer     NOT NULL DEFAULT 0,
  updated_at  timestamptz NOT NULL DEFAULT now()
);
```

(Check the existing ingest migration filenames first and follow their numbering.)

Implement `NextOffset`/`AdvanceOffset` in `services/ingest/internal/adapter/postgres/store.go` alongside the job methods:

```go
func (s *Store) NextOffset(ctx context.Context, sourceURL string) (int32, error) {
	var offset int32
	err := s.pool.QueryRow(ctx,
		`SELECT next_offset FROM source_cursors WHERE source_url = $1`, sourceURL).Scan(&offset)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return offset, err
}

func (s *Store) AdvanceOffset(ctx context.Context, sourceURL string, by int32) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO source_cursors (source_url, next_offset)
		VALUES ($1, $2)
		ON CONFLICT (source_url) DO UPDATE
		SET next_offset = source_cursors.next_offset + EXCLUDED.next_offset,
		    updated_at = now()`, sourceURL, by)
	return err
}
```

Match the receiver name and pool field to what the file already uses.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./services/ingest/... -v`
Expected: PASS.

- [ ] **Step 8: Expose it over RPC**

In `proto/ingest/v1/ingest.proto` add to `IngestService`:

```proto
  // Brings new material into the library when the feed is running low. Metadata
  // only: nothing is downloaded until someone presses play.
  rpc ExpandLibrary(ExpandLibraryRequest) returns (ExpandLibraryResponse);
```

```proto
message ExpandLibraryRequest {
  // Empty means "any topic".
  string topic = 1;
  // Videos to look beside, normally the most recently watched.
  repeated string seed_video_ids = 2;
}

message ExpandLibraryResponse {
  int32 videos_added = 1;
}
```

Run `make proto`, then add the handler to `services/ingest/internal/adapter/rpc/server.go` following the shape of the neighbouring handlers, and wire an `*usecase.Expander` through `services/ingest/cmd/ingest/main.go` — constructing `innertube.New(nil)` as the `RelatedSource` and the existing Postgres store as the `CursorStore`.

- [ ] **Step 9: Verify the build**

Run: `make check && go test ./...`
Expected: both succeed.

- [ ] **Step 10: Commit**

```bash
git add proto/ingest/ gen/ services/ingest/
git commit -m "Expand the library when the feed runs low"
```

---

### Task 5: Trigger expansion from the gateway

**Files:**
- Modify: `services/gateway/internal/api/router.go:139-194` (`handleFeed`)

**Interfaces:**
- Consumes: `recsysv1.GetFeedResponse.RemainingCount` (Task 2), `ingestv1.ExpandLibrary` (Task 4).

- [ ] **Step 1: Fire an expansion when the feed is nearly exhausted**

In `services/gateway/internal/api/router.go`, add above `handleFeed`:

```go
// expandThreshold is how few remaining videos count as "running low". Two pages
// of headroom is enough to refill before a scroller reaches the end, and the
// refill itself is metadata-only so it costs nothing on disk.
const expandThreshold = 48
```

and inside `handleFeed`, immediately after the impressions goroutine:

```go
	// Running low: go and find more. Fire-and-forget, because a viewer must
	// never wait on a network round trip to YouTube to get the page they asked
	// for — the new material lands in the next page instead.
	if ranked.Msg.GetRemainingCount() < expandThreshold {
		go g.expandLibrary(r.URL.Query().Get("topic"), ids)
	}
```

and add the method next to `recordImpressions`:

```go
func (g *Gateway) expandLibrary(topic string, seedVideoIDs []string) {
	ctx, cancel := contextWithTimeout(2 * time.Minute)
	defer cancel()

	// A handful of seeds is plenty; every one is a separate round trip.
	if len(seedVideoIDs) > 3 {
		seedVideoIDs = seedVideoIDs[:3]
	}

	resp, err := g.ingest.ExpandLibrary(ctx, connect.NewRequest(&ingestv1.ExpandLibraryRequest{
		Topic:        topic,
		SeedVideoIds: seedVideoIDs,
	}))
	if err != nil {
		g.logger.Warn("expand library", "topic", topic, "error", err)
		return
	}
	g.logger.Info("library expanded", "topic", topic, "added", resp.Msg.GetVideosAdded())
}
```

Add the `ingestv1` import if it is not already present in this file.

- [ ] **Step 2: Guard against overlapping expansions**

Two viewers scrolling at once would start two passes. Add to the `Gateway` struct:

```go
	// One expansion at a time. Concurrent passes would double the request rate
	// against YouTube for material the first pass is already fetching.
	expanding atomic.Bool
```

and make `expandLibrary` return immediately when it cannot claim the flag:

```go
	if !g.expanding.CompareAndSwap(false, true) {
		return
	}
	defer g.expanding.Store(false)
```

placed as the first lines of the method. Add `"sync/atomic"` to the imports.

- [ ] **Step 3: Verify the build**

Run: `make check && go test ./...`
Expected: both succeed.

- [ ] **Step 4: Verify end to end**

Run `scripts/dev.sh`. Note the current library size:

```bash
curl -s localhost:8080/api/storage | python3 -m json.tool
```

Open the home page and scroll to the bottom repeatedly.
Expected: the grid keeps loading; the gateway log prints `library expanded ... added=N` with N > 0; `videoCount` from the storage endpoint has grown. Scroll back up and confirm no video appears twice — this is the specific defect the snapshot exists to prevent, so check it deliberately rather than assuming.

- [ ] **Step 5: Commit**

```bash
git add services/gateway/
git commit -m "Go looking for more videos before the feed runs out"
```

---

### Task 6: Run the eviction sweep

`0001_init.sql:53-55` already carries an index built for exactly this query, and `pinned` / `last_accessed_at` already exist. Nothing has ever read them.

**Files:**
- Create: `services/catalog/internal/usecase/evict.go`
- Create: `services/catalog/internal/usecase/evict_test.go`
- Modify: `services/catalog/internal/domain/catalog.go` (port)
- Modify: `services/catalog/internal/adapter/postgres/repository.go` (query)
- Modify: `services/catalog/cmd/catalog/main.go` (start the loop)

**Interfaces:**
- Produces:
  - `domain.EvictionRepository` methods added to the existing repository port: `ListEvictionCandidates(ctx context.Context, downToBytes int64) ([]EvictionCandidate, error)` and `MarkEvicted(ctx context.Context, videoID string) error`
  - `type EvictionCandidate struct{ VideoID, MediaPath string; SizeBytes int64 }`
  - `NewEvictor(repo, mediaRoot string, highWatermark, lowWatermark int64, logger *slog.Logger) *Evictor`; `(*Evictor).Run(ctx)`; `(*Evictor).SweepOnce(ctx) (freedBytes int64, err error)`

- [ ] **Step 1: Write the failing test**

Create `services/catalog/internal/usecase/evict_test.go`:

```go
package usecase

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/lucnguyen/local-youtube/services/catalog/internal/domain"
)

type fakeEvictionRepo struct {
	usedBytes  int64
	candidates []domain.EvictionCandidate
	evicted    []string
}

func (f *fakeEvictionRepo) UsedBytes(context.Context) (int64, error) { return f.usedBytes, nil }

func (f *fakeEvictionRepo) ListEvictionCandidates(context.Context, int64) ([]domain.EvictionCandidate, error) {
	return f.candidates, nil
}

func (f *fakeEvictionRepo) MarkEvicted(_ context.Context, videoID string) error {
	f.evicted = append(f.evicted, videoID)
	return nil
}

func TestSweepDeletesLeastRecentlyAccessedUntilUnderTheLowWatermark(t *testing.T) {
	root := t.TempDir()
	// Three files of 100 bytes each, ordered oldest-accessed first by the repo.
	for _, name := range []string{"v1", "v2", "v3"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "1080p.mp4"), make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	repo := &fakeEvictionRepo{
		usedBytes: 300,
		candidates: []domain.EvictionCandidate{
			{VideoID: "v1", MediaPath: "v1/1080p.mp4", SizeBytes: 100},
			{VideoID: "v2", MediaPath: "v2/1080p.mp4", SizeBytes: 100},
			{VideoID: "v3", MediaPath: "v3/1080p.mp4", SizeBytes: 100},
		},
	}

	// Over the 250 high watermark; delete down to 150.
	evictor := NewEvictor(repo, root, 250, 150, slog.New(slog.NewTextHandler(io.Discard, nil)))

	freed, err := evictor.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if freed != 200 {
		t.Fatalf("freed = %d, want 200", freed)
	}
	if len(repo.evicted) != 2 || repo.evicted[0] != "v1" || repo.evicted[1] != "v2" {
		t.Fatalf("evicted = %v, want [v1 v2] — least recently accessed first", repo.evicted)
	}

	// The media file goes; nothing else does.
	if _, err := os.Stat(filepath.Join(root, "v1", "1080p.mp4")); !os.IsNotExist(err) {
		t.Error("v1 media file survived the sweep")
	}
	if _, err := os.Stat(filepath.Join(root, "v3", "1080p.mp4")); err != nil {
		t.Error("v3 was deleted even though the sweep had already reached the low watermark")
	}
}

func TestSweepDoesNothingBelowTheHighWatermark(t *testing.T) {
	repo := &fakeEvictionRepo{usedBytes: 100}
	evictor := NewEvictor(repo, t.TempDir(), 250, 150, slog.New(slog.NewTextHandler(io.Discard, nil)))

	freed, err := evictor.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if freed != 0 || len(repo.evicted) != 0 {
		t.Fatalf("swept below the watermark: freed=%d evicted=%v", freed, repo.evicted)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./services/catalog/internal/usecase/ -run TestSweep -v`
Expected: FAIL to build — `undefined: NewEvictor`.

- [ ] **Step 3: Add the domain types**

In `services/catalog/internal/domain/catalog.go`:

```go
// EvictionCandidate is a downloaded, unpinned video, offered oldest-accessed
// first. Only the media file is ever removed: metadata, thumbnail and watch
// history stay, so the grid can offer a one-click re-download rather than
// pretending the video never existed.
type EvictionCandidate struct {
	VideoID   string
	MediaPath string
	SizeBytes int64
}

// EvictionRepository is the slice of the repository the sweep needs. Kept
// narrow so the sweep can be tested without a database.
type EvictionRepository interface {
	UsedBytes(ctx context.Context) (int64, error)
	ListEvictionCandidates(ctx context.Context, downToBytes int64) ([]EvictionCandidate, error)
	MarkEvicted(ctx context.Context, videoID string) error
}
```

- [ ] **Step 4: Implement the evictor**

Create `services/catalog/internal/usecase/evict.go`:

```go
package usecase

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// sweepInterval is how often disk usage is checked. Hourly is frequent enough
// that the ceiling is never far away, and rare enough to be invisible.
const sweepInterval = time.Hour

// Evictor keeps the media directory under its budget.
//
// The disk is the hardest constraint in this system: 34 GiB total, roughly 25
// of which is available to media. Everything else in the design assumes
// something is enforcing that, and until now nothing was.
//
// Deleting only the media file — never the catalog row, the thumbnail or the
// history — is what makes eviction reversible. A reclaimed video keeps its
// place in the grid and offers to fetch itself again.
type Evictor struct {
	repo          domain.EvictionRepository
	mediaRoot     string
	highWatermark int64
	lowWatermark  int64
	logger        *slog.Logger
}

func NewEvictor(repo domain.EvictionRepository, mediaRoot string, highWatermark, lowWatermark int64, logger *slog.Logger) *Evictor {
	return &Evictor{
		repo:          repo,
		mediaRoot:     mediaRoot,
		highWatermark: highWatermark,
		lowWatermark:  lowWatermark,
		logger:        logger,
	}
}

func (e *Evictor) Run(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()

	for {
		if freed, err := e.SweepOnce(ctx); err != nil {
			e.logger.Error("eviction sweep", "error", err)
		} else if freed > 0 {
			e.logger.Info("eviction sweep freed space", "bytes", freed)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// SweepOnce deletes least-recently-accessed unpinned media until usage is back
// under the low watermark. Sweeping to a level below the trigger, rather than
// just under it, is what stops the sweep from running again on the next tick.
func (e *Evictor) SweepOnce(ctx context.Context) (int64, error) {
	used, err := e.repo.UsedBytes(ctx)
	if err != nil {
		return 0, err
	}
	if used <= e.highWatermark {
		return 0, nil
	}

	candidates, err := e.repo.ListEvictionCandidates(ctx, e.lowWatermark)
	if err != nil {
		return 0, err
	}

	var freed int64
	for _, c := range candidates {
		if used-freed <= e.lowWatermark {
			break
		}
		if c.MediaPath == "" {
			continue
		}

		path := filepath.Join(e.mediaRoot, c.MediaPath)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			// A file that cannot be removed must not stop the sweep, or one bad
			// path would let the disk fill anyway.
			e.logger.Warn("remove media file", "video", c.VideoID, "path", path, "error", err)
			continue
		}

		if err := e.repo.MarkEvicted(ctx, c.VideoID); err != nil {
			e.logger.Warn("mark evicted", "video", c.VideoID, "error", err)
			continue
		}
		freed += c.SizeBytes
	}
	return freed, nil
}
```

Add the domain import.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./services/catalog/internal/usecase/ -run TestSweep -v`
Expected: PASS.

- [ ] **Step 6: Implement the repository methods**

In `services/catalog/internal/adapter/postgres/repository.go`:

```go
func (r *Repository) UsedBytes(ctx context.Context) (int64, error) {
	var used int64
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(size_bytes), 0) FROM videos WHERE media_state = 'READY'`).Scan(&used)
	return used, err
}

// ListEvictionCandidates returns unpinned downloaded videos, least recently
// accessed first. This is the query the partial index in 0001_init.sql exists
// to serve.
func (r *Repository) ListEvictionCandidates(ctx context.Context, _ int64) ([]domain.EvictionCandidate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, media_path, size_bytes
		FROM videos
		WHERE media_state = 'READY' AND NOT pinned
		ORDER BY last_accessed_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.EvictionCandidate
	for rows.Next() {
		var c domain.EvictionCandidate
		if err := rows.Scan(&c.VideoID, &c.MediaPath, &c.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkEvicted keeps everything except the bytes: the row, the thumbnail and the
// history survive so the video can offer to fetch itself again.
func (r *Repository) MarkEvicted(ctx context.Context, videoID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE videos
		SET media_state = 'EVICTED', media_path = '', size_bytes = 0
		WHERE id = $1`, videoID)
	return err
}
```

Match the receiver and pool field names the file already uses.

- [ ] **Step 7: Start the sweep**

In `services/catalog/cmd/catalog/main.go`, next to where `mediaRoot` and `STORAGE_BUDGET_BYTES` are already read, add the two watermarks with the charter's values as defaults and start the loop:

```go
	// Charter §4: sweep above 20 GiB, down to 16 GiB.
	var (
		highWatermark int64 = 20 << 30
		lowWatermark  int64 = 16 << 30
	)
	if raw := os.Getenv("EVICTION_HIGH_BYTES"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			highWatermark = v
		}
	}
	if raw := os.Getenv("EVICTION_LOW_BYTES"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			lowWatermark = v
		}
	}

	evictor := usecase.NewEvictor(repo, mediaRoot, highWatermark, lowWatermark, logger)
	go evictor.Run(ctx)
```

Match the existing variable names for `repo`, `logger` and `ctx`.

- [ ] **Step 8: Verify the build and the whole suite**

Run: `make check && go test ./...`
Expected: both succeed.

- [ ] **Step 9: Verify the sweep does something real**

With the stack running, force a sweep by lowering the watermarks below current usage:

```bash
curl -s localhost:8080/api/storage | python3 -m json.tool   # note usedBytes
# restart catalog with, for example, usedBytes/2 and usedBytes/4:
EVICTION_HIGH_BYTES=<half> EVICTION_LOW_BYTES=<quarter> go run ./services/catalog/cmd/catalog
```

Expected: the log prints `eviction sweep freed space bytes=...`; `usedBytes` drops to roughly the low watermark; the evicted videos still appear in the grid, now showing "Removed — click to re-download" (`VideoCard.tsx:21-27` already renders this for `EVICTED`); a pinned video is untouched. Restore the real watermarks afterwards.

- [ ] **Step 10: Commit**

```bash
git add services/catalog/
git commit -m "Actually run the eviction sweep the schema was built for"
```

---

### Task 7: Update the charter

**Files:**
- Modify: `CLAUDE.md` §7, §8b

- [ ] **Step 1: Record the reversed decision**

Two statements in `CLAUDE.md` are now false and must be corrected rather than left to rot:

In §8b under "Quyết định đã bị đảo trong quá trình làm", add:

```
- **Feed**: từng chốt "topics.yaml là nguồn duy nhất của feed" → **đảo lại**: khi feed sắp cạn,
  gateway gọi `ExpandLibrary` để kéo thêm — đào sâu chính các source trong topics.yaml trước,
  rồi related qua InnerTube, cuối cùng mới là search. Lý do: cuộn vô tận là yêu cầu, mà 280 video
  thì hết sau ~12 trang. Thứ tự các lớp là có chủ đích — lớp đào sâu không thể hỏng, nên
  InnerTube vỡ thì feed vẫn vô tận, chỉ kém đa dạng.
- **Phân trang feed**: offset trên bảng xếp hạng vừa rank lại → **snapshot đông cứng theo phiên**
  (memory recsys, TTL 30 phút). Lý do: `recordImpressions` trừ điểm chính những video vừa hiện,
  nên trang sau rank trên bảng đã khác trang trước và sinh ra video trùng.
```

and update the "Chưa làm" list: remove item 3 (eviction job) and note the sweep is running.

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "Record that the feed reaches past topics.yaml now"
```

---

## Verification of the whole plan

```bash
make check
go test ./...
```

Then, with `scripts/dev.sh` running:

1. Scroll the home feed past 300 videos. No video appears twice; no end is reached.
2. `curl -s localhost:8080/api/storage` shows `videoCount` growing as you scroll.
3. The gateway log shows `library expanded` entries, and at most one expansion at a time.
4. Kill the network and scroll: the feed still pages through what is already ranked, and `expand library` warnings appear in the log without any page failing.
