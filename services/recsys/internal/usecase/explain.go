package usecase

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// Why a video is not in the feed at all.
//
// Kept as strings rather than an enum because they are read by a person holding
// a video id and asking where it went, and every one of them is a different
// question with a different answer. "Excluded" on its own is the answer that
// prompted this endpoint.
const (
	excludedTopicFilter      = "topic filter"
	excludedDisliked         = "disliked"
	excludedChannelSuppresed = "channel suppressed by dislikes"
	excludedMediaFailed      = "media failed"
	excludedMediaUnavailable = "media unavailable upstream"
	excludedMediaEvicted     = "media evicted"
	excludedWatchedEnough    = "already watched"
	excludedFinished         = "finished"
	excludedLanguage         = "language filter"
	excludedNoPublishDate    = "no publish date"
	excludedTooOld           = "published over a year ago"
)

// rankInputs is everything the per-video score depends on beyond the video.
//
// Bundled so that scoring is one function rather than a loop body. Before this
// the only way to find out why a video scored what it did was to read the loop
// and do the arithmetic by hand — which is how a uniform shuffle sat on top of
// the whole calculation for months without anybody being able to point at it.
type rankInputs struct {
	profile         domain.UserProfile
	watchAffinity   WatchAffinity
	sessionAffinity WatchAffinity
	likes           ReactionAffinity
	dislikes        ReactionAffinity
	retention       map[string]float32
	coverage        map[string]int
	suppressed      func(channelID string) bool
	topic           string
	languages       []string
	now             time.Time
	tuning          resolvedTuning
}

// ScoreBreakdown is one video's score with its working shown.
//
// The components are recorded as they are applied, so that the sum of the
// additive terms times the multipliers is the final score by construction. An
// explanation that can disagree with the ranking is worse than no explanation,
// which is why there is no second implementation of any of this.
type ScoreBreakdown struct {
	VideoID string
	// Empty when the video is in the feed; otherwise why it is not, and every
	// other field is meaningless.
	Excluded string

	RecencyBoost     float64
	ContinueWatching float64
	NeverWatched     float64
	Bounced          float64
	Subscribed       float64

	LongTermAffinity   float64
	SessionAffinity    float64
	CombinedAffinity   float64
	AffinityMultiplier float64

	DiscoveryBase     float64
	ExplorationBonus  float64
	LikeAffinity      float64
	DislikeAffinity   float64
	Retention         float64
	ImpressionPenalty float64

	PublishedAgePenalty float64
	FreshnessBoost      float64
	TimesShown          int

	Score  float64
	Reason domain.Reason
	Slot   feedSlot
	// Where it actually lands in the feed, or -1 when it does not appear. Not
	// derivable from the score: the quota interleaves by slot and the per-channel
	// cap defers, so position and score routinely disagree — which is the single
	// most useful thing this endpoint has to say.
	Position int
}

// Names for the shares, for the explanation only. The zero value is "other",
// which is a real state: bounced videos belong to no share.
func (s feedSlot) String() string {
	switch s {
	case slotContinueWatching:
		return "continue_watching"
	case slotRewatch:
		return "rewatch"
	case slotSubscribed:
		return "subscribed"
	case slotAffinity:
		return "affinity"
	case slotDiscovery:
		return "discovery"
	case slotFreshSubscribed:
		return "fresh_subscribed"
	default:
		return "other"
	}
}

// Component names, so the map keys are stable enough to grep for across runs.
func (b ScoreBreakdown) Components() map[string]float64 {
	return map[string]float64{
		"recency_boost":         b.RecencyBoost,
		"continue_watching":     b.ContinueWatching,
		"never_watched":         b.NeverWatched,
		"bounced":               b.Bounced,
		"subscribed":            b.Subscribed,
		"affinity_long_term":    b.LongTermAffinity,
		"affinity_session":      b.SessionAffinity,
		"affinity_combined":     b.CombinedAffinity,
		"affinity_multiplier":   b.AffinityMultiplier,
		"discovery_base":        b.DiscoveryBase,
		"exploration_bonus":     b.ExplorationBonus,
		"times_shown":           float64(b.TimesShown),
		"like_affinity":         b.LikeAffinity,
		"dislike_affinity":      b.DislikeAffinity,
		"retention":             b.Retention,
		"impression_penalty":    b.ImpressionPenalty,
		"published_age_penalty": b.PublishedAgePenalty,
		"freshness_boost":       b.FreshnessBoost,
	}
}

// scoreVideo is the whole of the feed's opinion about one video.
//
// Extracted from rankAll unchanged in behaviour. The order of operations is
// load-bearing — the affinity multiplier applies to the watch-state terms and
// not to likes, and the age and freshness multipliers apply to everything — so
// the sequence here is the specification, not an implementation detail.
func scoreVideo(f domain.VideoFeatures, in rankInputs) ScoreBreakdown {
	out := ScoreBreakdown{VideoID: f.VideoID}
	now := in.now

	if !matchesTopic(f, in.topic) {
		out.Excluded = excludedTopicFilter
		return out
	}
	if _, disliked := in.profile.Disliked[f.VideoID]; disliked {
		out.Excluded = excludedDisliked
		return out
	}
	if in.suppressed(f.ChannelID) {
		out.Excluded = excludedChannelSuppresed
		return out
	}
	switch f.MediaState {
	case "MEDIA_STATE_FAILED":
		out.Excluded = excludedMediaFailed
		return out
	case "MEDIA_STATE_UNAVAILABLE":
		// Members-only, private or removed. It stays reachable through search
		// and the channel page — the library does hold it, and knowing why it
		// cannot be watched is worth something — but putting it on the home
		// page is inviting a press on something that cannot open.
		out.Excluded = excludedMediaUnavailable
		return out
	}
	// MEDIA_STATE_EVICTED is deliberately not excluded. It was, for as long as
	// "no local copy" meant "pressing this does nothing" — but the instant tier
	// plays an undownloaded video straight away while the copy is fetched
	// behind it, so an evicted card behaves like any other. Holding to the old
	// rule cost 359 videos across the library, and 104 of the 402 in Music.

	fraction, opened := in.profile.WatchedFraction[f.VideoID]
	if opened && fraction >= watchedEnoughThreshold {
		out.Excluded = excludedWatchedEnough
		return out
	}
	// When a language filter is set, skip videos whose language is unknown or
	// does not match one of the allowed codes.
	if len(in.languages) > 0 {
		if f.Language == "" {
			out.Excluded = excludedLanguage
			return out
		}
		allowed := false
		for _, lang := range in.languages {
			if strings.EqualFold(f.Language, lang) {
				allowed = true
				break
			}
		}
		if !allowed {
			out.Excluded = excludedLanguage
			return out
		}
	}
	// Videos without a publish date are skipped — the feed only ranks content
	// whose age is known. AddedAt is a fallback for the age penalty on the
	// ranking side, not for surfacing in the feed.
	if f.PublishedAt.IsZero() || f.PublishedAt.Unix() <= 0 {
		out.Excluded = excludedNoPublishDate
		return out
	}
	// The age limit belongs to the open feed only. It exists to stop old
	// material drifting up a page nobody asked a question of — but picking a
	// topic chip is a question, and the answer to "show me music" is not
	// "music from this year".
	//
	// Music is where it showed: 170 of the library's music videos are over a
	// year old against 148 under it, so the rule hid more than half of it, where
	// an ordinary topic loses 7%.
	if in.topic == "" && now.Sub(f.PublishedAt).Hours()/24 > in.tuning.maxPublishedAgeDays {
		out.Excluded = excludedTooOld
		return out
	}

	out.RecencyBoost = weightRecentlyAdded * recencyBoost(f.AddedAt, now, in.tuning.recencyHalfLifeDay)
	score := out.RecencyBoost
	out.Reason = domain.ReasonRecentlyAdded
	// Assigned alongside the reason but not from it. The watch states come first
	// because they describe a video the viewer has already committed to; where it
	// came from only decides the rest.
	out.Slot = slotOther

	switch {
	case isContinueWatching(fraction):
		out.ContinueWatching = weightContinueWatching
		score += out.ContinueWatching
		out.Reason = domain.ReasonContinueWatching
		out.Slot = slotContinueWatching
	case isWatched(fraction):
		// Already finished — don't suggest again.
		out.Excluded = excludedFinished
		return out
	case opened:
		// Opened and left almost immediately.
		out.Bounced = -penaltyBounced
		score += out.Bounced
		out.Reason = domain.ReasonBounced
	default:
		out.NeverWatched = weightNeverWatched
		score += out.NeverWatched
		out.Reason = domain.ReasonNeverWatched
	}

	if in.profile.Subscribed[f.ChannelID] {
		out.Subscribed = weightSubscribed
		score += out.Subscribed
		if out.Reason == domain.ReasonRecentlyAdded || out.Reason == domain.ReasonNeverWatched {
			out.Reason = domain.ReasonSubscribedChannel
		}
		if out.Slot == slotOther {
			out.Slot = slotSubscribed
		}
		// A new upload takes the fresh share instead, overriding the ordinary
		// subscribed one. Continue-watching still wins: a video the viewer is
		// halfway through is not news, whenever it was published.
		if out.Slot != slotContinueWatching && now.Sub(f.PublishedAt) < in.tuning.freshnessWindow {
			out.Slot = slotFreshSubscribed
		}
	}

	// Affinity is multiplicative, not additive. Additive affinity gave a video
	// from an unwatched channel ~3.0 and an affinity-matched video ~5.5 — a 1.8×
	// gap too narrow to survive quota interleaving, where each bucket picks
	// independently. Multiplicative runs 0.5× to 2.5×, which is a 5× gap: enough
	// that affinity content dominates its bucket and discovery stays in its own.
	//
	// Only watch-based affinity goes in the multiplier — channels and topics,
	// both normalised 0..1. Likes stay additive: they are rare (9 vs 2,045 watch
	// signals in this library) and already weight 2.0.
	out.LongTermAffinity = blendAffinity(
		in.watchAffinity.Channels[f.ChannelID], in.watchAffinity.TopicScore(f))
	combinedAffinity := out.LongTermAffinity

	// What they came for today, blended evenly with what they usually want.
	//
	// Even, rather than either extreme, because both extremes are wrong in a way
	// that is easy to demonstrate. All history and the feed cannot notice that
	// somebody has spent the last hour on one subject. All session and the feed
	// becomes the up-next rail — one video decides the whole page, and a viewer
	// who opened something out of curiosity has to watch their way back out of
	// it. Halves leave the session clearly visible while two years of watching
	// still holds the page together.
	if len(in.profile.SessionWatched) > 0 {
		out.SessionAffinity = blendAffinity(
			in.sessionAffinity.Channels[f.ChannelID], in.sessionAffinity.TopicScore(f))
		combinedAffinity = in.tuning.sessionBlend*out.SessionAffinity +
			(1-in.tuning.sessionBlend)*combinedAffinity
	}
	out.CombinedAffinity = combinedAffinity
	out.AffinityMultiplier = 0.5 + 2.0*combinedAffinity

	// Discovery: outside the viewer's affinity and not a channel they chose to
	// follow. Gets a dedicated reason so the quota can reserve a window for
	// unfamiliar material.
	//
	// The complement of that test is the affinity slot: not subscribed, but
	// matching what this viewer watches. It had no share of its own before, which
	// is why "more of what I like" could not be turned up or down — those videos
	// were spread across the never-watched and recently-added buckets, mixed in
	// with everything else that happened to be new.
	if !in.profile.Subscribed[f.ChannelID] {
		if combinedAffinity < discoveryAffinityThreshold {
			out.Reason = domain.ReasonDiscovery
			out.DiscoveryBase = weightDiscoveryBase
			score += out.DiscoveryBase
			// Within the discovery share, prefer what has had the least exposure.
			// Every showing decays the bonus, so a video offered repeatedly and
			// never opened drifts back down and lets the next one through — which
			// is the difference between a share reserved for unfamiliar material
			// and a share that shows the same unfamiliar material forever.
			//
			// Confined to discovery deliberately. Applied across the whole feed it
			// would penalise precisely the videos the viewer likes best, since
			// those are the ones shown most.
			out.TimesShown = in.coverage[f.VideoID]
			out.ExplorationBonus = weightUnseen *
				math.Exp(-float64(out.TimesShown)/explorationDecay)
			score += out.ExplorationBonus
			if out.Slot == slotOther {
				out.Slot = slotDiscovery
			}
		} else if out.Slot == slotOther {
			out.Slot = slotAffinity
		}
	}

	score *= out.AffinityMultiplier

	out.LikeAffinity = weightLikeAffinity * in.likes.Score(f)
	score += out.LikeAffinity
	// The other half of the same sentence. Outside the multiplier for the same
	// reason likes are: reactions are rare next to watch signals, and scaling a
	// rejection by how much the viewer likes the channel it came from is not what
	// the rejection said.
	out.DislikeAffinity = -weightDislikeAffinity * in.dislikes.Score(f)
	score += out.DislikeAffinity
	out.Retention = weightRetention * float64(in.retention[f.VideoID])
	score += out.Retention

	if in.profile.RecentImpressions[f.VideoID] {
		out.ImpressionPenalty = -penaltyImpression
		score += out.ImpressionPenalty
	}

	out.PublishedAgePenalty = publishedAgePenalty(f.PublishedAt, f.AddedAt, now)
	score *= out.PublishedAgePenalty
	// Fresh videos get a boost, which is how breaking news surfaces without
	// anybody in the household having watched it first.
	//
	// There used to be a further ×3.0 here for fresh videos from subscribed
	// channels. It was doing the job slotFreshSubscribed now does, and doing it by
	// brute force: the multiplier applied in every bucket the video landed in, so
	// for two days after a favourite channel posted, the page was that channel and
	// little else — bounded only by the per-channel cap. A reserved share of a
	// tenth says the same thing and says how much.
	out.FreshnessBoost = freshnessBoost(f.PublishedAt, f.ViewCount, now, in.tuning.freshnessWindow)
	score *= out.FreshnessBoost

	out.Score = score
	return out
}

// blendAffinity weighs a channel score against a topic score by their weights.
// Both arrive normalised to 0..1, so the result is too.
func blendAffinity(channel, topic float64) float64 {
	return (weightChannelAffinity*channel + weightTopicAffinity*topic) /
		(weightChannelAffinity + weightTopicAffinity)
}

// ExplainFeed returns the full working for every video, ranked and rejected
// alike, in the order the feed would present them.
//
// There is no UI for this and there does not need to be one. It exists because
// every constant in this package has been tuned by looking at a page and forming
// an impression, and an impression cannot distinguish "the weight is wrong" from
// "the weight is right and something downstream is discarding it" — which is
// exactly the distinction that was missed.
func (r *Ranker) ExplainFeed(
	ctx context.Context, userID, topic string, mix FeedMix, languages []string, tuning Tuning,
) ([]ScoreBreakdown, error) {
	features, in, err := r.buildInputs(ctx, userID, topic, languages, tuning)
	if err != nil {
		return nil, err
	}

	out := make([]ScoreBreakdown, 0, len(features))
	for _, f := range features {
		out = append(out, scoreVideo(f, in))
	}

	// Included videos first, in the order the feed would serve them, then the
	// rejected ones. Position in the feed is most of what somebody is asking
	// about, and it cannot be read off the score alone once the quota has run.
	position := map[string]int{}
	ranked, err := r.rankAll(ctx, userID, topic, mix, languages, tuning)
	if err != nil {
		return nil, err
	}
	for i, v := range ranked {
		position[v.VideoID] = i
	}
	for i := range out {
		if pos, ok := position[out[i].VideoID]; ok {
			out[i].Position = pos
		} else {
			out[i].Position = -1
		}
	}
	sortBreakdowns(out, position)
	return out, nil
}

func sortBreakdowns(out []ScoreBreakdown, position map[string]int) {
	sort.SliceStable(out, func(i, j int) bool {
		posA, inA := position[out[i].VideoID]
		posB, inB := position[out[j].VideoID]
		switch {
		case inA && inB:
			return posA < posB
		case inA != inB:
			return inA
		default:
			return out[i].VideoID < out[j].VideoID
		}
	})
}
