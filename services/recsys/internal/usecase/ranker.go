// Package usecase implements the Phase 1 ranking heuristic.
//
// Deliberately not machine learning. With a few hundred self-imported videos
// and a handful of users there is not enough signal for a model to beat
// explicit rules, and every score here can be explained to the person looking
// at the grid — which a learned model could not do.
package usecase

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

const (
	impressionWindow = 24 * time.Hour

	// Score weights. Kept as named constants so tuning is a readable diff.
	weightContinueWatching = 3.0
	weightNeverWatched     = 1.5
	weightSubscribed       = 1.2
	weightRecentlyAdded    = 1.0
	weightChannelAffinity  = 1.0
	// Likes are a deliberate statement and outweigh passive watch affinity, but
	// stay below "continue watching": an unfinished video is a stronger claim on
	// attention than a preference.
	weightLikeAffinity = 2.0
	weightRewatch      = 0.3
	penaltyImpression      = 2.0
	penaltyDisliked        = 5.0
	// Same-channel dominance is what makes the "Next" rail feel coherent.
	weightSameChannel  = 2.5
	weightSharedTags   = 1.5
	recencyHalfLifeDay = 14.0
)

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

func (r *Ranker) RecordSignal(ctx context.Context, s domain.Signal) error {
	if s.OccurredAt.IsZero() {
		s.OccurredAt = r.now()
	}
	return r.store.AppendSignal(ctx, s)
}

func (r *Ranker) RecordImpressions(ctx context.Context, userID string, videoIDs []string) error {
	if userID == "" || len(videoIDs) == 0 {
		return nil
	}
	return r.store.RecordImpressions(ctx, userID, videoIDs)
}

// recencyBoost decays from 1 towards 0 with a fortnight half-life, so a freshly
// ingested video leads the grid without permanently owning it.
func recencyBoost(addedAt time.Time, now time.Time) float64 {
	days := now.Sub(addedAt).Hours() / 24
	if days < 0 {
		days = 0
	}
	return math.Exp(-days / recencyHalfLifeDay)
}

func isContinueWatching(fraction float32) bool { return fraction > 0.02 && fraction <= 0.95 }
func isWatched(fraction float32) bool          { return fraction > 0.95 }

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

func (r *Ranker) rankAll(ctx context.Context, userID, topic string) ([]domain.RankedVideo, error) {
	features, err := r.features.ListVideoFeatures(ctx)
	if err != nil {
		return nil, err
	}
	profile, err := r.store.BuildProfile(ctx, userID, impressionWindow)
	if err != nil {
		return nil, err
	}

	affinity := channelAffinity(features, profile.WatchedFraction)
	likes := buildLikeAffinity(features, profile.Liked)

	now := r.now()
	ranked := make([]domain.RankedVideo, 0, len(features))

	for _, f := range features {
		if !matchesTopic(f, topic) {
			continue
		}
		if profile.Disliked[f.VideoID] {
			continue
		}

		fraction := profile.WatchedFraction[f.VideoID]
		score := weightRecentlyAdded * recencyBoost(f.AddedAt, now)
		reason := domain.ReasonRecentlyAdded

		switch {
		case isContinueWatching(fraction):
			score += weightContinueWatching
			reason = domain.ReasonContinueWatching
		case isWatched(fraction):
			// Rewatching is allowed but must not crowd out fresh material.
			score += weightRewatch
			reason = domain.ReasonRewatch
		default:
			score += weightNeverWatched
			reason = domain.ReasonNeverWatched
		}

		if profile.Subscribed[f.ChannelID] {
			score += weightSubscribed
			if reason == domain.ReasonRecentlyAdded || reason == domain.ReasonNeverWatched {
				reason = domain.ReasonSubscribedChannel
			}
		}

		score += weightChannelAffinity * affinity[f.ChannelID]
		score += weightLikeAffinity * likes.Score(f)

		if profile.RecentImpressions[f.VideoID] {
			score -= penaltyImpression
		}

		ranked = append(ranked, domain.RankedVideo{VideoID: f.VideoID, Score: score, Reason: reason})
	}

	sortRanked(ranked)
	// The mix applies to the feed, which is what someone browses. Up-next is a
	// different question — "what follows this?" — and deliberately keeps its
	// pure same-channel-first ordering.
	return applyDiscoveryQuota(ranked), nil
}

func (r *Ranker) GetUpNext(ctx context.Context, userID, currentVideoID, channelFilter string, pageSize int32) ([]domain.RankedVideo, error) {
	features, err := r.features.ListVideoFeatures(ctx)
	if err != nil {
		return nil, err
	}
	profile, err := r.store.BuildProfile(ctx, userID, impressionWindow)
	if err != nil {
		return nil, err
	}

	affinity := channelAffinity(features, profile.WatchedFraction)

	var current *domain.VideoFeatures
	for i := range features {
		if features[i].VideoID == currentVideoID {
			current = &features[i]
			break
		}
	}

	now := r.now()
	ranked := make([]domain.RankedVideo, 0, len(features))

	for _, f := range features {
		if f.VideoID == currentVideoID {
			continue
		}
		if channelFilter != "" && f.ChannelID != channelFilter {
			continue
		}
		if profile.Disliked[f.VideoID] {
			continue
		}

		score := weightRecentlyAdded * recencyBoost(f.AddedAt, now)
		reason := domain.ReasonRecentlyAdded

		if current != nil {
			if f.ChannelID == current.ChannelID {
				score += weightSameChannel
				reason = domain.ReasonSameChannel
			}
			if overlap := countOverlap(f.Topics, current.Topics) +
				countOverlap(f.Hashtags, current.Hashtags); overlap > 0 {
				score += weightSharedTags * float64(overlap)
				if reason != domain.ReasonSameChannel {
					reason = domain.ReasonSharedTags
				}
			}
		}

		fraction := profile.WatchedFraction[f.VideoID]
		if isContinueWatching(fraction) {
			score += weightContinueWatching
			reason = domain.ReasonContinueWatching
		} else if isWatched(fraction) {
			// Already-seen videos sink but stay available.
			score -= penaltyDisliked / 2
		}

		score += weightChannelAffinity * affinity[f.ChannelID]

		ranked = append(ranked, domain.RankedVideo{VideoID: f.VideoID, Score: score, Reason: reason})
	}

	return sortAndPage(ranked, pageSize, 0), nil
}

// channelAffinity turns watch history into a per-channel preference in the 0..1
// range. It is computed here rather than in the store because only the catalog
// projection knows which channel a video belongs to — the signal store must
// never need that fact.
func channelAffinity(features []domain.VideoFeatures, watched map[string]float32) map[string]float64 {
	if len(watched) == 0 {
		return map[string]float64{}
	}

	channelOf := make(map[string]string, len(features))
	for _, f := range features {
		channelOf[f.VideoID] = f.ChannelID
	}

	totals := map[string]float64{}
	var max float64
	for videoID, fraction := range watched {
		channelID, ok := channelOf[videoID]
		if !ok {
			continue
		}
		totals[channelID] += float64(fraction)
		if totals[channelID] > max {
			max = totals[channelID]
		}
	}

	if max == 0 {
		return map[string]float64{}
	}
	for channelID := range totals {
		totals[channelID] /= max
	}
	return totals
}

func matchesTopic(f domain.VideoFeatures, topic string) bool {
	if topic == "" || strings.EqualFold(topic, "all") {
		return true
	}
	for _, t := range f.Topics {
		if strings.EqualFold(t, topic) {
			return true
		}
	}
	return false
}

func countOverlap(a, b []string) int {
	set := make(map[string]struct{}, len(b))
	for _, s := range b {
		set[strings.ToLower(s)] = struct{}{}
	}
	n := 0
	for _, s := range a {
		if _, ok := set[strings.ToLower(s)]; ok {
			n++
		}
	}
	return n
}

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

func sortAndPage(ranked []domain.RankedVideo, pageSize, offset int32) []domain.RankedVideo {
	sortRanked(ranked)

	if pageSize <= 0 || pageSize > 100 {
		pageSize = 24
	}
	start := int(offset)
	if start > len(ranked) {
		start = len(ranked)
	}
	end := start + int(pageSize)
	if end > len(ranked) {
		end = len(ranked)
	}
	return ranked[start:end]
}
