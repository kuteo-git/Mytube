# Recommendation Signals Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Like change what the feed offers, and stop the feed from collapsing into one subject once it does.

**Architecture:** Two halves that must ship together.

`profile.Liked` is built by the signal store (`store.go:104`) and read by nothing — `GetFeed` uses only `Disliked`, to exclude. The first half turns likes into an affinity over the three things a video actually carries: its topic, its channel, and its hashtags, weighted 1.0 / 0.8 / 0.5 and accumulated across likes.

The second half is a fixed discovery quota — 30% never watched, 25% recently added, 20% subscribed channel, 15% continue watching, 10% rewatch — as specified in CLAUDE.md §6 P2. It is not optional polish. Like affinity and subscription weighting are both forces pulling the feed toward what is already familiar; a pure score ranking under both, over a library of a few hundred videos, converges on one subject within a few dozen likes. The quota is the only thing holding it open.

**Tech Stack:** Go, pure functions over the existing `domain.UserProfile` and `domain.VideoFeatures`. No new dependencies, no schema change, no proto change.

## Global Constraints

- **All source code, identifiers, comments, commit messages and in-app UI copy MUST be in English.** (CLAUDE.md §4b)
- **Heuristic, not machine learning.** With ~300 videos and 5 users there is not enough signal for a model to beat explicit rules, and every score must be explainable to the person looking at the grid. (CLAUDE.md §6)
- `domain` imports no DB, HTTP or framework.
- **Quota ratios are fixed by the charter:** 30 / 25 / 20 / 15 / 10, in that order of bucket. Do not retune them in this plan.
- **Depends on:** `2026-07-28-infinite-feed-and-eviction.md` Tasks 1–2. This plan modifies `rankAll`, which that plan creates. Do not start until it has landed.
- Verification: `go test ./...` and `make check`.

---

### Task 1: Turn likes into an affinity over topic, channel and hashtag

**Files:**
- Create: `services/recsys/internal/usecase/affinity.go`
- Create: `services/recsys/internal/usecase/affinity_test.go`
- Modify: `services/recsys/internal/usecase/ranker.go` (weights, `rankAll`)

**Interfaces:**
- Consumes: `domain.UserProfile.Liked map[string]bool`, `domain.VideoFeatures{VideoID, ChannelID, Topics, Hashtags}`.
- Produces:
  - `type LikeAffinity struct{ Topics, Channels, Hashtags map[string]float64 }`
  - `buildLikeAffinity(features []domain.VideoFeatures, liked map[string]bool) LikeAffinity`
  - `(LikeAffinity).Score(f domain.VideoFeatures) float64`

- [ ] **Step 1: Write the failing test**

Create `services/recsys/internal/usecase/affinity_test.go`:

```go
package usecase

import (
	"math"
	"testing"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

func TestLikeAffinityScoresTopicAboveChannelAboveHashtag(t *testing.T) {
	features := []domain.VideoFeatures{
		{VideoID: "liked", ChannelID: "chanA", Topics: []string{"Music"}, Hashtags: []string{"live"}},
		{VideoID: "sameTopic", ChannelID: "chanB", Topics: []string{"Music"}},
		{VideoID: "sameChannel", ChannelID: "chanA", Topics: []string{"Tech"}},
		{VideoID: "sameHashtag", ChannelID: "chanC", Topics: []string{"Tech"}, Hashtags: []string{"live"}},
		{VideoID: "unrelated", ChannelID: "chanD", Topics: []string{"Gaming"}},
	}

	affinity := buildLikeAffinity(features, map[string]bool{"liked": true})

	topic := affinity.Score(features[1])
	channel := affinity.Score(features[2])
	hashtag := affinity.Score(features[3])
	none := affinity.Score(features[4])

	if !(topic > channel && channel > hashtag && hashtag > none) {
		t.Fatalf("ordering wrong: topic=%v channel=%v hashtag=%v none=%v",
			topic, channel, hashtag, none)
	}
	if none != 0 {
		t.Errorf("an unrelated video scored %v, want 0", none)
	}
	if math.Abs(topic-1.0) > 1e-9 {
		t.Errorf("one like on one topic should score 1.0, got %v", topic)
	}
	if math.Abs(channel-0.8) > 1e-9 {
		t.Errorf("channel weight = %v, want 0.8", channel)
	}
	if math.Abs(hashtag-0.5) > 1e-9 {
		t.Errorf("hashtag weight = %v, want 0.5", hashtag)
	}
}

func TestLikeAffinityAccumulatesAcrossLikes(t *testing.T) {
	features := []domain.VideoFeatures{
		{VideoID: "l1", ChannelID: "chanA", Topics: []string{"Music"}},
		{VideoID: "l2", ChannelID: "chanB", Topics: []string{"Music"}},
		{VideoID: "candidate", ChannelID: "chanZ", Topics: []string{"Music"}},
	}

	one := buildLikeAffinity(features, map[string]bool{"l1": true})
	two := buildLikeAffinity(features, map[string]bool{"l1": true, "l2": true})

	if two.Score(features[2]) <= one.Score(features[2]) {
		t.Fatalf("two likes on a topic must outweigh one: %v vs %v",
			two.Score(features[2]), one.Score(features[2]))
	}
}

func TestVideoWithMultipleMatchesScoresHigherThanEither(t *testing.T) {
	features := []domain.VideoFeatures{
		{VideoID: "liked", ChannelID: "chanA", Topics: []string{"Music"}},
		{VideoID: "both", ChannelID: "chanA", Topics: []string{"Music"}},
		{VideoID: "topicOnly", ChannelID: "chanB", Topics: []string{"Music"}},
	}

	affinity := buildLikeAffinity(features, map[string]bool{"liked": true})
	if affinity.Score(features[1]) <= affinity.Score(features[2]) {
		t.Fatal("matching on both topic and channel must beat matching on topic alone")
	}
}

func TestNoLikesMeansNoInfluence(t *testing.T) {
	features := []domain.VideoFeatures{{VideoID: "a", ChannelID: "chanA", Topics: []string{"Music"}}}
	affinity := buildLikeAffinity(features, map[string]bool{})
	if affinity.Score(features[0]) != 0 {
		t.Fatal("a user who has liked nothing must get no affinity boost")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./services/recsys/internal/usecase/ -run TestLikeAffinity -v`
Expected: FAIL to build — `undefined: buildLikeAffinity`.

- [ ] **Step 3: Implement it**

Create `services/recsys/internal/usecase/affinity.go`:

```go
package usecase

import (
	"strings"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// What a like is taken to mean, in descending order of confidence.
//
// A like says "more like this", and a video carries three claims about what
// "this" is. The topic is the strongest: it is the label the library was
// organised by, and it is what a person means by "I like this kind of thing".
// The channel is nearly as strong but overlaps with Subscribe, which says the
// same thing more explicitly. Hashtags are the weakest — they are whatever the
// uploader typed.
const (
	likeWeightTopic   = 1.0
	likeWeightChannel = 0.8
	likeWeightHashtag = 0.5
)

// LikeAffinity is how much a user's likes point at each topic, channel and
// hashtag. Recomputed per request from the raw signals: there is no model here
// and nothing is stored, so a like changes the next grid immediately and the
// reason can always be explained.
type LikeAffinity struct {
	Topics   map[string]float64
	Channels map[string]float64
	Hashtags map[string]float64
}

func buildLikeAffinity(features []domain.VideoFeatures, liked map[string]bool) LikeAffinity {
	affinity := LikeAffinity{
		Topics:   map[string]float64{},
		Channels: map[string]float64{},
		Hashtags: map[string]float64{},
	}
	if len(liked) == 0 {
		return affinity
	}

	for _, f := range features {
		if !liked[f.VideoID] {
			continue
		}
		affinity.Channels[f.ChannelID] += likeWeightChannel
		for _, topic := range f.Topics {
			affinity.Topics[strings.ToLower(topic)] += likeWeightTopic
		}
		for _, tag := range f.Hashtags {
			affinity.Hashtags[strings.ToLower(tag)] += likeWeightHashtag
		}
	}
	return affinity
}

// Score is how much this user's likes argue for showing a given video. A video
// matching on several axes scores the sum, which is the intended behaviour:
// the same channel *and* the same topic is a stronger argument than either.
func (a LikeAffinity) Score(f domain.VideoFeatures) float64 {
	score := a.Channels[f.ChannelID]
	for _, topic := range f.Topics {
		score += a.Topics[strings.ToLower(topic)]
	}
	for _, tag := range f.Hashtags {
		score += a.Hashtags[strings.ToLower(tag)]
	}
	return score
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./services/recsys/internal/usecase/ -run TestLikeAffinity -v`
Expected: PASS (all four).

- [ ] **Step 5: Feed it into ranking**

In `services/recsys/internal/usecase/ranker.go`, add to the weight constants:

```go
	// Likes are a deliberate statement and outweigh passive watch affinity, but
	// stay below "continue watching": an unfinished video is a stronger claim on
	// attention than a preference.
	weightLikeAffinity = 2.0
```

In `rankAll` (created by the infinite-feed plan), after `affinity := channelAffinity(...)`:

```go
	likes := buildLikeAffinity(features, profile.Liked)
```

and inside the scoring loop, after the existing `score += weightChannelAffinity * affinity[f.ChannelID]`:

```go
		score += weightLikeAffinity * likes.Score(f)
```

- [ ] **Step 6: Verify**

Run: `go test ./services/recsys/... -v && make check`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add services/recsys/internal/usecase/affinity.go \
        services/recsys/internal/usecase/affinity_test.go \
        services/recsys/internal/usecase/ranker.go
git commit -m "Make Like mean something to the feed"
```

---

### Task 2: Hold the feed open with a discovery quota

**Files:**
- Create: `services/recsys/internal/usecase/quota.go`
- Create: `services/recsys/internal/usecase/quota_test.go`
- Modify: `services/recsys/internal/usecase/ranker.go` (`rankAll` returns quota-interleaved output)

**Interfaces:**
- Consumes: `domain.RankedVideo` carrying `Reason`, which `rankAll` already assigns.
- Produces: `applyDiscoveryQuota(ranked []domain.RankedVideo) []domain.RankedVideo` — reorders, never drops.

- [ ] **Step 1: Write the failing test**

Create `services/recsys/internal/usecase/quota_test.go`:

```go
package usecase

import (
	"testing"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

func repeated(reason domain.Reason, n int, prefix string) []domain.RankedVideo {
	out := make([]domain.RankedVideo, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, domain.RankedVideo{
			VideoID: prefix + string(rune('a'+i)),
			Reason:  reason,
			Score:   float64(n - i),
		})
	}
	return out
}

// The failure this prevents: likes and subscriptions both pull the feed toward
// the familiar, and a pure score ordering under both collapses to one subject.
func TestQuotaKeepsUnwatchedVideosInTheFirstPageEvenWhenTheyScoreLowest(t *testing.T) {
	var ranked []domain.RankedVideo
	// Twenty high-scoring rewatches, then unwatched videos scoring below them.
	ranked = append(ranked, repeated(domain.ReasonRewatch, 20, "rw")...)
	ranked = append(ranked, repeated(domain.ReasonNeverWatched, 20, "nw")...)

	got := applyDiscoveryQuota(ranked)

	firstPage := got[:24]
	unwatched := 0
	for _, v := range firstPage {
		if v.Reason == domain.ReasonNeverWatched {
			unwatched++
		}
	}
	// The 30% bucket over a 24-slot page is 7 entries.
	if unwatched < 7 {
		t.Fatalf("first page had %d never-watched videos, want at least 7", unwatched)
	}
}

func TestQuotaDropsNothing(t *testing.T) {
	var ranked []domain.RankedVideo
	ranked = append(ranked, repeated(domain.ReasonRewatch, 5, "rw")...)
	ranked = append(ranked, repeated(domain.ReasonNeverWatched, 3, "nw")...)
	ranked = append(ranked, repeated(domain.ReasonContinueWatching, 2, "cw")...)

	got := applyDiscoveryQuota(ranked)

	if len(got) != len(ranked) {
		t.Fatalf("got %d videos, want %d — the quota reorders, it never drops",
			len(got), len(ranked))
	}
	seen := map[string]bool{}
	for _, v := range got {
		if seen[v.VideoID] {
			t.Fatalf("video %s appeared twice", v.VideoID)
		}
		seen[v.VideoID] = true
	}
}

func TestQuotaFallsBackToScoreWhenABucketIsEmpty(t *testing.T) {
	// A brand-new user has nothing watched, so four of the five buckets are
	// empty. The page must still fill.
	ranked := repeated(domain.ReasonNeverWatched, 30, "nw")

	got := applyDiscoveryQuota(ranked)
	if len(got) != 30 {
		t.Fatalf("got %d, want 30", len(got))
	}
	if got[0].VideoID != "nwa" {
		t.Errorf("highest-scoring video should still lead, got %s", got[0].VideoID)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./services/recsys/internal/usecase/ -run TestQuota -v`
Expected: FAIL to build — `undefined: applyDiscoveryQuota`.

- [ ] **Step 3: Implement it**

Create `services/recsys/internal/usecase/quota.go`:

```go
package usecase

import "github.com/lucnguyen/local-youtube/services/recsys/internal/domain"

// The mix, fixed by CLAUDE.md §6 P2.
//
// This exists because scoring alone does not stay open. Likes and subscriptions
// both push the feed toward what is already familiar, and over a library of a
// few hundred videos that convergence happens within a few dozen likes — far
// faster than it would on a catalogue of millions. The quota reserves room for
// material the score would otherwise bury.
//
// It reorders and never drops: everything ranked is still reachable by
// scrolling, just not in pure score order.
var quotaBuckets = []struct {
	reason domain.Reason
	share  float64
}{
	{domain.ReasonNeverWatched, 0.30},
	{domain.ReasonRecentlyAdded, 0.25},
	{domain.ReasonSubscribedChannel, 0.20},
	{domain.ReasonContinueWatching, 0.15},
	{domain.ReasonRewatch, 0.10},
}

// quotaWindow is the span the ratios apply over. Matching the default page size
// means the mix is visible on the first screen rather than emerging over
// several.
const quotaWindow = 24

func applyDiscoveryQuota(ranked []domain.RankedVideo) []domain.RankedVideo {
	if len(ranked) == 0 {
		return ranked
	}

	// Split by reason, preserving the score order within each bucket.
	byReason := make(map[domain.Reason][]domain.RankedVideo, len(quotaBuckets))
	var other []domain.RankedVideo
	known := make(map[domain.Reason]bool, len(quotaBuckets))
	for _, b := range quotaBuckets {
		known[b.reason] = true
	}
	for _, v := range ranked {
		if known[v.Reason] {
			byReason[v.Reason] = append(byReason[v.Reason], v)
			continue
		}
		other = append(other, v)
	}

	out := make([]domain.RankedVideo, 0, len(ranked))
	for len(out) < len(ranked) {
		before := len(out)

		for _, bucket := range quotaBuckets {
			take := int(bucket.share * quotaWindow)
			if take < 1 {
				take = 1
			}
			available := byReason[bucket.reason]
			if take > len(available) {
				take = len(available)
			}
			out = append(out, available[:take]...)
			byReason[bucket.reason] = available[take:]
		}

		// Buckets can empty at different rates. Whatever is left over — reasons
		// outside the quota, or the remainder of an over-full bucket — fills the
		// gap in score order rather than leaving the page short.
		if len(out) == before {
			for _, bucket := range quotaBuckets {
				out = append(out, byReason[bucket.reason]...)
				byReason[bucket.reason] = nil
			}
			out = append(out, other...)
			other = nil
			break
		}
	}

	// Anything the loop did not reach.
	for _, bucket := range quotaBuckets {
		out = append(out, byReason[bucket.reason]...)
	}
	return append(out, other...)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./services/recsys/internal/usecase/ -run TestQuota -v`
Expected: PASS (all three).

- [ ] **Step 5: Apply it to the feed only**

In `rankAll`, replace the closing:

```go
	sortRanked(ranked)
	return ranked, nil
```

with:

```go
	sortRanked(ranked)
	// The mix applies to the feed, which is what someone browses. Up-next is a
	// different question — "what follows this?" — and deliberately keeps its
	// pure same-channel-first ordering.
	return applyDiscoveryQuota(ranked), nil
```

Do **not** apply the quota in `GetUpNext`.

- [ ] **Step 6: Verify the whole suite**

Run: `go test ./... && make check`
Expected: PASS.

- [ ] **Step 7: Verify the behaviour is real**

With the stack running:

```bash
curl -s 'localhost:8080/api/feed?pageSize=24' | \
  python3 -c 'import json,sys,collections; print(collections.Counter(v["reason"] for v in json.load(sys.stdin)["videos"]))'
```

Expected: a spread across several reasons rather than one dominating. Then like five videos from a single topic through the UI and run it again.

Expected: videos from that topic rise, **and** the reason spread stays mixed — roughly a quarter to a third of the page is still `NEVER_WATCHED`. If the page becomes single-reason, the quota is not being applied; check that `rankAll` and not only `GetFeedPage` was changed.

- [ ] **Step 8: Commit**

```bash
git add services/recsys/internal/usecase/quota.go \
        services/recsys/internal/usecase/quota_test.go \
        services/recsys/internal/usecase/ranker.go
git commit -m "Hold the feed open with a fixed discovery mix"
```

---

### Task 3: Confirm Dislike behaves as decided — do not change it

The settled decision was: Dislike removes a video from the feed, but leaves it findable through search and the channel page.

Reading the code, **this is already true**, and this task exists to verify that rather than to change anything. `rankAll` skips disliked videos (`ranker.go:94`) and so does `GetUpNext` (`ranker.go:163`), while `SearchVideos` and `ListChannelVideos` in catalog never consult recsys at all — they cannot filter by a signal they do not see, which is the service boundary working as intended.

**Files:** none modified.

- [ ] **Step 1: Verify the feed drops it**

With the stack running, dislike a video through the UI, then:

```bash
curl -s 'localhost:8080/api/feed?pageSize=100' | grep -c '<the video id>'
```

Expected: `0`.

- [ ] **Step 2: Verify search still finds it**

```bash
curl -s 'localhost:8080/api/search?q=<a word from its title>' | grep -c '<the video id>'
```

Expected: at least `1`.

- [ ] **Step 3: Verify the channel page still lists it**

```bash
curl -s "localhost:8080/api/channels/<its channel id>/videos" | grep -c '<the video id>'
```

Expected: at least `1`.

- [ ] **Step 4: Record the finding**

If all three hold, add to `CLAUDE.md` §6:

```
- **Dislike**: loại khỏi feed và up-next, **vẫn tìm được qua search và trang kênh**.
  Không phải tính năng phải làm — nó đúng sẵn nhờ ranh giới service: catalog không
  nhìn thấy signal của recsys nên không thể lọc theo nó.
```

If any of the three fails, **stop and report** rather than patching: a failure here means the service boundary is leakier than the architecture claims, and that is worth understanding before it is papered over.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md
git commit -m "Record how Dislike actually behaves"
```

---

### Task 4: Update the charter

**Files:**
- Modify: `CLAUDE.md` §6, §8b

- [ ] **Step 1: Move the mix from planned to done**

In `CLAUDE.md` §6, the line `**P2:** grid trộn có chủ ý ~30% chưa xem / ...` describes this as Phase 2 work. Mark it as shipped, and add the like affinity:

```
- **P1 (đã làm):** grid trộn 30% chưa xem / 25% mới / 20% kênh theo dõi / 15% xem dở /
  10% xem lại; chống lặp impression 24h
- **Like:** cộng affinity theo topic 1.0 / kênh 0.8 / hashtag 0.5, cộng dồn qua từng like.
  Đưa lên P1 vì like mà không đổi gì thì là nút chết.
```

In §8b, remove item 2 from "Chưa làm" — affinity theo chủ đề và tỉ lệ khám phá cố định giờ đã có.

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "Record that the feed mix and like affinity are in"
```

---

## Verification of the whole plan

```bash
go test ./...
make check
```

Behavioural check, which is the one that matters:

1. Like five videos on one topic.
2. Reload the home feed. Videos from that topic are visibly more prominent.
3. The reason spread on the first page is still mixed — no single reason owns the page.
4. Dislike a video: it leaves the feed, and search still finds it.
