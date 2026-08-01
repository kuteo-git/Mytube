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
	// Continue-watching is a claim on attention, not a claim on the first page.
	// Halved from 3.0 because even with the quota at 10% the old weight made
	// partially-watched videos dominate every bucket they fell into.
	weightContinueWatching = 1.5
	weightNeverWatched     = 1.5
	weightSubscribed       = 1.2
	weightRecentlyAdded    = 1.0
	// Raised from 1.0 because affinity is now multiplicative: these weights set
	// how fast the multiplier climbs as a viewer watches a channel or topic.
	weightChannelAffinity = 2.0
	// Likes are a deliberate statement and outweigh passive watch affinity, but
	// stay below "continue watching": an unfinished video is a stronger claim on
	// attention than a preference.
	weightLikeAffinity = 2.0
	weightRewatch      = 0.3
	penaltyImpression  = 2.0
	penaltyDisliked    = 5.0
	// Opening a video and leaving within the first few per cent is a judgement,
	// not an absence of one. Before this existed such a video fell through to
	// "never watched" and collected that bucket's boost, so the surest way to
	// keep something in the feed was to reject it. The penalty is smaller than
	// a dislike, which is deliberate: leaving early is weaker evidence than
	// saying so, and the video stays reachable by search and on its channel.
	penaltyBounced = 2.5
	// How much of a video counts as having given it a chance.
	bounceThreshold = 0.02
	// Retention is a fraction, so this is the most a perfectly held video can
	// gain. Kept below the like weight: what everyone finishes still loses to
	// what this viewer said they wanted.
	weightRetention = 1.5
	// Watching is a weaker statement than liking, so the same topic match earns
	// less. It accumulates across a viewing history, which is the point — fifty
	// unremarked cooking videos should say what one like says.
	weightTopicAffinity = 2.0
	// Subject and channel weigh the same, and per shared tag rather than once,
	// so two videos sharing a topic and a hashtag outrank one merely sharing a
	// channel.
	//
	// CLAUDE.md §6 used to rank channel above subject, and with the cap on how
	// much of the rail one channel may take, that left the remaining slots going
	// to whatever general taste preferred — an Entertainment video followed by
	// six from the viewer's favourite musician. What people mean by "related" is
	// the subject; the channel is one way of sharing it, not a better one.
	weightSameChannel = 2.5
	weightSharedTags  = 2.5
	// How much of everything that is not relatedness survives into up-next.
	//
	// The feed asks "what should I watch?", where taste and quality should
	// dominate. Up-next asks "what follows this?", where they must not: at full
	// strength, channel affinity plus topic affinity plus retention outweigh a
	// shared topic, and the rail stops being about the video on screen.
	//
	// Retention is damped along with taste, and on this library that matters
	// more than it looks. It is meant to be a property of the video — how far
	// the average viewer gets — but averaged across a single user it is that
	// user's own history again, so leaving it undamped counted the same
	// preference twice.
	//
	// A third leaves these able to order two equally related candidates without
	// overturning relatedness itself.
	upNextTasteDamping = 0.35
	// Larger than same-channel and shared-tags together, deliberately. Those
	// two are what make a pair of videos by one artist each other's top
	// suggestion, and nothing smaller than their sum can break the loop.
	penaltyRecentlyWatched = 8.0
	recencyHalfLifeDay     = 5.0 // was 14.0: fresher turnover, fed by grilling session 2026-08-01
	// Base score added to discovery videos so they compete within their quota
	// bucket despite having no affinity signal. Kept below weightNeverWatched
	// so the never-watched bucket still prefers videos the viewer might like.
	weightDiscoveryBase = 0.8
	// Combined watch-based affinity (channel + topic, normalised 0..1) below
	// which a video from an unsubscribed channel is classified as discovery.
	// 0.15 means "essentially no meaningful connection to anything watched."
	discoveryAffinityThreshold = 0.15
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

const publishedHalfLifeDay = 365

// maxPublishedAgeDays is a hard filter: videos published (or, when the date is
// unknown, added to the library) more than this many days ago do not appear on
// the home page. A penalty is not enough because quota reordering (§quota.go)
// slots them by reason regardless of absolute score.
const maxPublishedAgeDays = 365

// publishedAgePenalty penalises videos that were old when they entered the
// library. A video published five years ago on YouTube is not bad content, but
// it should not crowd out something uploaded last week just because they were
// both imported on the same day.
func publishedAgePenalty(publishedAt, addedAt, now time.Time) float64 {
	if publishedAt.IsZero() {
		if addedAt.IsZero() {
			return 1.0
		}
		days := now.Sub(addedAt).Hours() / 24
		if days < 0 {
			days = 0
		}
		return math.Exp(-days / publishedHalfLifeDay)
	}
	days := now.Sub(publishedAt).Hours() / 24
	if days < 0 {
		days = 0
	}
	p := math.Exp(-days / publishedHalfLifeDay)
	return p
}

func isContinueWatching(fraction float32) bool { return fraction > 0.02 && fraction <= 0.95 }
func isWatched(fraction float32) bool          { return fraction > 0.95 }

const watchedEnoughThreshold = 0.85

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

	watchAffinity := buildWatchAffinity(features, profile.WatchedFraction)
	likes := buildLikeAffinity(features, profile.Liked)

	// How well each video holds an audience, across everyone. A blip reading
	// this must not empty the feed: without it every video simply scores as
	// though nobody had watched anything, which is where the library started.
	retention, err := r.store.VideoRetention(ctx)
	if err != nil {
		retention = map[string]float32{}
	}

	now := r.now()
	ranked := make([]domain.RankedVideo, 0, len(features))

	for _, f := range features {
		if !matchesTopic(f, topic) {
			continue
		}
		if profile.Disliked[f.VideoID] {
			continue
		}
		if f.MediaState == "MEDIA_STATE_FAILED" {
			continue
		}
		if f.MediaState == "MEDIA_STATE_EVICTED" {
			continue
		}

		fraction, opened := profile.WatchedFraction[f.VideoID]
		if opened && fraction >= watchedEnoughThreshold {
			continue
		}
		hasPub := !f.PublishedAt.IsZero() && f.PublishedAt.Unix() > 0
		if hasPub && now.Sub(f.PublishedAt).Hours()/24 > maxPublishedAgeDays {
			continue
		}
		if !hasPub && !f.AddedAt.IsZero() && now.Sub(f.AddedAt).Hours()/24 > maxPublishedAgeDays {
			continue
		}
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
		case opened:
			// Opened and left almost immediately.
			score -= penaltyBounced
			reason = domain.ReasonBounced
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

		// Affinity is multiplicative, not additive. Additive affinity gave a
		// video from an unwatched channel ~3.0 and an affinity-matched video
		// ~5.5 — a 1.8× gap too narrow to survive quota interleaving, where
		// each bucket picks independently. Multiplicative runs 0.5× to 2.5×,
		// which is a 5× gap: enough that affinity content dominates its bucket
		// and discovery stays in its own.
		//
		// Only watch-based affinity goes in the multiplier — channels and
		// topics, both normalised 0..1. Likes stay additive: they are rare
		// (9 vs 2,045 watch signals in this library) and already weight 2.0.
		channelAff := watchAffinity.Channels[f.ChannelID]
		topicAff := watchAffinity.TopicScore(f)
		combinedAffinity := (weightChannelAffinity*channelAff + weightTopicAffinity*topicAff) /
			(weightChannelAffinity + weightTopicAffinity)
		affinityMultiplier := 0.5 + 2.0*combinedAffinity

		// Discovery: outside the viewer's affinity and not a channel they
		// chose to follow. Gets a dedicated reason so the quota (12%) can
		// reserve a small window for unfamiliar material.
		if combinedAffinity < discoveryAffinityThreshold && !profile.Subscribed[f.ChannelID] {
			reason = domain.ReasonDiscovery
			score += weightDiscoveryBase
		}

		score *= affinityMultiplier

		score += weightLikeAffinity * likes.Score(f)
		score += weightRetention * float64(retention[f.VideoID])

		if profile.RecentImpressions[f.VideoID] {
			score -= penaltyImpression
		}

		score *= publishedAgePenalty(f.PublishedAt, f.AddedAt, now)
		if !f.PublishedAt.IsZero() && now.Sub(f.PublishedAt) > 365*24*time.Hour {
			score -= 4.0
		}

		ranked = append(ranked, domain.RankedVideo{VideoID: f.VideoID, Score: score, Reason: reason})
	}

	sortRanked(ranked)

	channelOf := make(map[string]string, len(features))
	for _, f := range features {
		channelOf[f.VideoID] = f.ChannelID
	}

	// Two different mixes, because they answer different questions. The reason
	// quota keeps unfamiliar material on the page; the channel cap keeps one
	// source from being the page. A heavily watched channel satisfies every
	// reason at once, so the first cannot do the second's job.
	//
	// Both apply to the feed, which is what someone browses. Up-next is a
	// different question — "what follows this?" — and deliberately keeps its
	// pure same-channel-first ordering.
	return applyChannelDiversity(applyDiscoveryQuota(ranked), channelOf,
		maxPerChannelPerWindow, quotaWindow), nil
}

// MostWatched is the "played the most" collection, ordered by time spent.
func (r *Ranker) MostWatched(ctx context.Context, userID string, limit int32) ([]domain.RankedVideo, error) {
	return r.store.MostWatched(ctx, userID, limit)
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

	watchAffinity := buildWatchAffinity(features, profile.WatchedFraction)
	retention, err := r.store.VideoRetention(ctx)
	if err != nil {
		retention = map[string]float32{}
	}

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

		fraction, opened := profile.WatchedFraction[f.VideoID]
		switch {
		case isContinueWatching(fraction):
			// Damped like the rest of the taste terms. An unfinished video is a
			// strong claim on attention in the feed, where the question is what
			// to watch; it is a poor answer to what follows the video already
			// playing. Undamped it outscored same-channel and same-topic
			// combined, which is how an Entertainment video came to be followed
			// by four half-watched songs.
			score += upNextTasteDamping * weightContinueWatching
			reason = domain.ReasonContinueWatching
		case isWatched(fraction):
			// Already-seen videos sink but stay available.
			score -= penaltyDisliked / 2
		case opened:
			// Offering back something abandoned seconds ago is the most visible
			// way for a "next" rail to look broken.
			score -= penaltyBounced
			reason = domain.ReasonBounced
		}

		// Anything watched in the last few hours is a bad answer to "what
		// follows this?", however well it matches.
		//
		// Same-channel and shared-topic between them are worth more than four
		// points here, so two videos by one artist each rank first in the
		// other's suggestions and pressing next twice returns you to where you
		// started. Observed on this library as an inescapable two-video loop.
		// The penalty has to exceed that combined bonus to break it, which is
		// why it is the largest number in this function.
		if profile.RecentlyWatched[f.VideoID] {
			score -= penaltyRecentlyWatched
		}

		// General taste, damped hard.
		//
		// This rail answers "what follows this?", and CLAUDE.md §6 fixes the
		// order it must answer in: same channel, then same tag, then general
		// affinity. At full weight affinity does not come third — it wins.
		// Observed: an Entertainment video from a gaming channel offered twenty
		// consecutive music videos, none sharing its channel or its topic,
		// purely because that is what this viewer watches most. Damping keeps
		// taste as the tie-breaker the charter says it is.
		score += upNextTasteDamping * (weightChannelAffinity*watchAffinity.Channels[f.ChannelID] +
			weightTopicAffinity*watchAffinity.TopicScore(f) +
			weightRetention*float64(retention[f.VideoID]))

		ranked = append(ranked, domain.RankedVideo{VideoID: f.VideoID, Score: score, Reason: reason})
	}

	sortRanked(ranked)

	channelOf := make(map[string]string, len(features))
	for _, f := range features {
		channelOf[f.VideoID] = f.ChannelID
	}

	// The rail should stay on the subject without staying on one channel.
	//
	// Relatedness puts the current video's own channel above everything else,
	// which on its own made the rail twenty of twenty from that channel — the
	// right subject, but a dead end. Capping it lets the other channels covering
	// the same topic through, which is what "related" ought to have meant.
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 24
	}
	// Applied after the sort and in place of sortAndPage, which re-sorts by
	// score and would put the current channel straight back on top.
	return capPerChannel(ranked, channelOf, maxPerChannelUpNext, int(pageSize)), nil
}

// WatchAffinity is what a viewing history says about someone's taste, without
// them ever pressing a button.
//
// Likes already produce a preference, but almost nobody likes anything: this
// library holds 9 likes against 2,045 watch signals. Reading taste from
// watching is what makes the feed respond to how the library is actually used
// rather than to the rare deliberate gesture.
//
// Each axis is scaled to 0..1 against its own strongest entry, so the two
// cannot be compared to each other and neither can run away with the score.
type WatchAffinity struct {
	Channels map[string]float64
	Topics   map[string]float64
}

// buildWatchAffinity accumulates watched fractions per channel and per topic.
//
// Computed here rather than in the store because only the catalog projection
// knows which channel or topic a video belongs to — the signal store must never
// need that fact, and asking it to would put a join across a service boundary.
//
// Weighting by fraction rather than counting openings is the whole point: a
// video watched to the end argues for its channel far more than one abandoned
// after ten seconds, and counting would make those identical.
func buildWatchAffinity(
	features []domain.VideoFeatures, watched map[string]float32,
) WatchAffinity {
	affinity := WatchAffinity{
		Channels: map[string]float64{},
		Topics:   map[string]float64{},
	}
	if len(watched) == 0 {
		return affinity
	}

	byID := make(map[string]domain.VideoFeatures, len(features))
	for _, f := range features {
		byID[f.VideoID] = f
	}

	for videoID, fraction := range watched {
		feature, ok := byID[videoID]
		if !ok {
			continue
		}
		affinity.Channels[feature.ChannelID] += float64(fraction)
		for _, topic := range feature.Topics {
			affinity.Topics[strings.ToLower(topic)] += float64(fraction)
		}
	}

	normaliseToUnit(affinity.Channels)
	normaliseToUnit(affinity.Topics)
	return affinity
}

// TopicScore is how much this viewer's watching argues for a video's topics.
//
// The strongest matching topic, not the sum of them. Summing rewards a video
// for carrying more labels than its neighbours rather than for being a better
// match: anything tagged both "Music" and "Vietnamese music" collected the
// affinity twice and outscored an equally well-liked video that happened to
// carry one topic. Each axis is already normalised to 0..1, and taking the best
// match keeps this one bounded by that.
func (a WatchAffinity) TopicScore(f domain.VideoFeatures) float64 {
	var best float64
	for _, topic := range f.Topics {
		if score := a.Topics[strings.ToLower(topic)]; score > best {
			best = score
		}
	}
	return best
}

// normaliseToUnit divides a map by its largest value, in place.
//
// Without it these totals grow without bound as someone keeps watching, and a
// long-standing viewer's affinity would eventually swamp every other term in
// the score.
func normaliseToUnit(totals map[string]float64) {
	var max float64
	for _, value := range totals {
		if value > max {
			max = value
		}
	}
	if max == 0 {
		return
	}
	for key := range totals {
		totals[key] /= max
	}
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
