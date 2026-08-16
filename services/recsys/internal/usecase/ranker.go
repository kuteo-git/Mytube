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
	weightSubscribed       = 2.5
	weightRecentlyAdded    = 1.0
	// Raised from 1.0 because affinity is now multiplicative: these weights set
	// how fast the multiplier climbs as a viewer watches a channel or topic.
	weightChannelAffinity = 2.0
	// Likes are a deliberate statement and outweigh passive watch affinity, but
	// stay below "continue watching": an unfinished video is a stronger claim on
	// attention than a preference.
	weightLikeAffinity = 2.0
	// A dislike reads the same three axes as a like but is trusted about a
	// third as far. A like is usually approval of a kind of thing; a dislike is
	// usually aimed at this video — its thumbnail, its title, the two seconds
	// that were enough. So it tilts the feed away from a subject rather than
	// emptying it, which is what a matching weight would do.
	weightDislikeAffinity = 0.7
	weightRewatch         = 0.3
	penaltyImpression     = 2.0
	// Already-seen videos sink but stay available.
	//
	// Named for what reads it. It was penaltyDisliked = 5.0, halved at its only
	// use — and no disliked video ever reached it, because those are skipped
	// outright several lines earlier. The number is unchanged.
	penaltyAlreadyWatched = 2.5
	// Suppressing a channel takes both a floor and a share: at least this many
	// of its videos turned down, and at least this much of the channel.
	channelDislikeFloor = 3
	channelDislikeShare = 0.3
	// And a count that stands on its own, whatever the share says.
	//
	// The share is measured against everything the channel has in the library,
	// which quietly made large channels unsuppressible: NoCopyrightSounds sat at
	// 162 videos, so twenty rejections came to 12% and needed **forty-nine** to
	// count. Worse, every scan added more of them — the denominator grew, so
	// pressing "not interested" again moved the share *down*. The control could
	// not be made to work by using it more, which is the one thing a viewer will
	// try.
	//
	// Eight was too few. It is an ordinary rate of "not this one" on a channel
	// with forty videos, and it took whole channels for it: Igor Presnyakov and
	// Drumeo, both turned down 8 times out of about 42, disappeared from the
	// library — as did Tinh te and Vox Weather at 9. Between them they held 143
	// of the 402 videos in Music, which is most of why that feed came to 27
	// videos and felt like the same page every time.
	//
	// Twenty still catches NoCopyrightSounds, the case this ceiling was written
	// for, at 26 rejections. It is high enough that reaching it means going out
	// of one's way, which is what a verdict on a channel should take.
	//
	// The share below is left alone: a small channel turned down three times out
	// of five is a different statement, and 30% of it says so.
	channelDislikeCeiling = 20
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
	// What a video never shown to anybody earns inside the discovery share, and
	// how fast that fades as it is offered.
	//
	// The weight sits just above weightDiscoveryBase so that novelty orders the
	// bucket without overturning the modest affinity signal that is still in
	// there. The decay is measured in showings: at three the bonus is a third of
	// its full value, at ten it is gone. Three is about a week of pages for one
	// household — long enough to be a fair trial, short enough that a video
	// nobody wants stops asking.
	weightUnseen     = 1.5
	explorationDecay = 3.0
	// Videos published within this window get a freshness boost proportional to
	// their view count, so breaking news surfaces immediately without needing
	// anyone in the household to have watched it first.
	freshnessWindow = 48 * time.Hour
	// How strongly freshness pushes a video up. Multiplied by recency decay
	// (linear from 1.0 to 0 over the window) and the view-count factor.
	weightFreshness = 3.0
	// View-count cap for the freshness log factor, so one viral video does not
	// dominate the entire first page.
	maxFreshnessViewCount = 1_000_000
	// How much of the affinity multiplier is decided by the current sitting
	// rather than by the whole watch history. See the blend in rankAll.
	// How far back a sitting reaches, and how many of its videos are read, are
	// decided by the query that builds SessionWatched — see the store.
	sessionBlend = 0.5
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
	if err := r.store.AppendSignal(ctx, s); err != nil {
		return err
	}

	// Watching something is the one signal that should visibly change the feed
	// before the viewer asks it to. Dropping their frozen orderings is what lets
	// it: the next time Home loads it ranks again, with the last few minutes of
	// watching in the profile.
	//
	// The bounce threshold is the filter. Watch signals are appended every few
	// seconds of playback and again the instant something is opened, so reacting
	// to all of them would re-rank on a video the viewer looked at and closed —
	// which is not a statement of interest, and is already scored as the opposite.
	if s.Type == domain.SignalWatch && s.WatchedFraction > bounceThreshold {
		r.snapshots.InvalidateUser(s.UserID)
	}
	return nil
}

func (r *Ranker) RecordImpressions(ctx context.Context, userID string, videoIDs []string) error {
	if userID == "" || len(videoIDs) == 0 {
		return nil
	}
	return r.store.RecordImpressions(ctx, userID, videoIDs)
}

// recencyBoost decays from 1 towards 0 with a fortnight half-life, so a freshly
// ingested video leads the grid without permanently owning it.
func recencyBoost(addedAt time.Time, now time.Time, halfLifeDays float64) float64 {
	days := now.Sub(addedAt).Hours() / 24
	if days < 0 {
		days = 0
	}
	return math.Exp(-days / halfLifeDays)
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
func (r *Ranker) GetFeedPage(
	ctx context.Context,
	userID, topic, snapshotID string,
	pageSize, offset int32,
	mix FeedMix,
	languages []string,
	tuning Tuning,
) (FeedPage, error) {
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 24
	}

	fresh, err := r.rankAll(ctx, userID, topic, mix, languages, tuning)
	if err != nil {
		return FeedPage{}, err
	}

	key := userID + "|" + topic
	ordering, ok := r.snapshots.Get(snapshotID)
	if !ok {
		// The named ordering is gone, or none was named. Before minting a new
		// one, take the session this viewer is already reading.
		//
		// Without this, every first-page request built a fresh ordering — and a
		// client refetching an infinite query asks for *all* of its pages, page
		// one with no token and page two with the token it already had. Page one
		// then came from a new ordering while page two was still reading the old,
		// and the two spliced together repeat whatever they have in common.
		// Measured: four duplicates in the first forty-eight videos.
		if existing, found := r.snapshots.Latest(key); found {
			snapshotID = existing
		} else {
			snapshotID = r.snapshots.Put(key, fresh)
		}
		ordering, _ = r.snapshots.Get(snapshotID)
	}
	// New material belongs at the tail of whichever ordering is being served,
	// behind everything the viewer has already scrolled past.
	if r.snapshots.Append(snapshotID, fresh) > 0 {
		ordering, _ = r.snapshots.Get(snapshotID)
	}

	// The offset is never rewound. It used to be reset to zero whenever a new
	// ordering was built, which handed a client asking for page three the whole
	// of page one again — the duplication itself, rather than a guard against it.

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

// freshnessBoost returns a multiplier for videos published within the freshness
// window, scaled by view count. A video published an hour ago with high views
// gets the strongest boost; one published 23h ago with no views gets none.
// Videos outside the window or with no published date are unchanged (1.0).
func freshnessBoost(publishedAt time.Time, viewCount int64, now time.Time, window time.Duration) float64 {
	if publishedAt.IsZero() {
		return 1.0
	}
	age := now.Sub(publishedAt)
	if age >= window {
		return 1.0
	}
	// Linear decay from 1.0 (now) to 0.0 (at the freshness boundary).
	recency := 1.0 - age.Seconds()/window.Seconds()
	// Log-scale view count, paginated so one viral video doesn't dominate.
	// Zero views still gets a floor so breaking news with no known count
	// still surfaces — it just earns less than a proven video would.
	viewFactor := math.Log1p(float64(viewCount)) / math.Log1p(maxFreshnessViewCount)
	if viewFactor < 0.2 {
		viewFactor = 0.2
	}
	if viewFactor > 1.0 {
		viewFactor = 1.0
	}
	return 1.0 + weightFreshness*recency*viewFactor
}

// buildInputs gathers everything the score depends on that is not the video.
//
// Split out of rankAll so that ExplainFeed scores through exactly the same
// values. Two code paths computing the same profile would eventually disagree,
// and an explanation that disagrees with the feed is worse than none.
func (r *Ranker) buildInputs(
	ctx context.Context, userID, topic string, languages []string, tuning Tuning,
) ([]domain.VideoFeatures, rankInputs, error) {
	features, err := r.features.ListVideoFeatures(ctx)
	if err != nil {
		return nil, rankInputs{}, err
	}
	profile, err := r.store.BuildProfile(ctx, userID, impressionWindow)
	if err != nil {
		return nil, rankInputs{}, err
	}

	now := r.now()

	// How well each video holds an audience, across everyone. A blip reading
	// this must not empty the feed: without it every video simply scores as
	// though nobody had watched anything, which is where the library started.
	retention, err := r.store.VideoRetention(ctx)
	if err != nil {
		retention = map[string]float32{}
	}

	// How much of a chance each video has already had. Same failure posture as
	// retention: without it every video looks unseen, which is where the feed
	// started and is not worse than no feed.
	coverage, err := r.store.ImpressionCoverage(ctx)
	if err != nil {
		coverage = map[string]int{}
	}

	// Count dislikes per channel, against how many of that channel's videos
	// there are to dislike. Both are needed: the count alone treated three out
	// of five and three out of two hundred as the same verdict, and suppressed
	// the channel either way. The first is a viewer telling you what they think
	// of a channel; the second is three ordinary rejections in a library they
	// have been using for months.
	//
	// Subscribed channels are immune: following a channel is a deliberate
	// statement that overrides passive dislike.
	dislikedPerChannel := map[string]int{}
	videosPerChannel := map[string]int{}
	for _, f := range features {
		videosPerChannel[f.ChannelID]++
		if _, disliked := profile.Disliked[f.VideoID]; disliked {
			dislikedPerChannel[f.ChannelID]++
		}
	}
	suppressed := func(channelID string) bool {
		if profile.Subscribed[channelID] {
			return false
		}
		count := dislikedPerChannel[channelID]
		total := videosPerChannel[channelID]
		// Enough rejections is a verdict on its own. The share below still
		// catches the small channel turned down three times out of five; this
		// catches the large one turned down again and again, which the share
		// alone never could.
		if count >= channelDislikeCeiling {
			return true
		}
		return count >= channelDislikeFloor &&
			total > 0 &&
			float64(count)/float64(total) >= channelDislikeShare
	}

	return features, rankInputs{
		profile:       profile,
		watchAffinity: buildWatchAffinity(features, profile.WatchedFraction, profile.WatchedAt, now),
		// The same computation over the last few videos only. Built the same way
		// because it is the same question asked over a shorter window, and two
		// different notions of "what this person likes" would be one too many.
		//
		// No decay applied to this one: everything in it happened within hours,
		// so ageing it would only be arithmetic on a number already too small to
		// matter, and the session is supposed to speak loudly while it lasts.
		sessionAffinity: buildWatchAffinity(features, profile.SessionWatched, nil, now),
		// Which channels have been in front of this viewer lately, and which
		// have gone quiet. See rotation.go for why Home needed it.
		rotation: buildChannelRotation(features, profile, now),
		// Learned from the history rather than configured, so it corrects itself:
		// start watching a language and it stops being filtered out.
		watchedLanguages: buildWatchedLanguages(features, profile.WatchedFraction),
		likes:            buildLikeAffinity(features, profile.Liked),
		// What the dislikes say beyond the videos they were pressed on. Subtracted
		// rather than added, and aged — see buildDislikeAffinity.
		dislikes:   buildDislikeAffinity(features, profile.Disliked, now),
		retention:  retention,
		coverage:   coverage,
		suppressed: suppressed,
		topic:      topic,
		languages:  languages,
		now:        now,
		tuning:     tuning.resolve(),
	}, nil
}

func (r *Ranker) rankAll(
	ctx context.Context,
	userID, topic string,
	mix FeedMix,
	languages []string,
	tuning Tuning,
) ([]domain.RankedVideo, error) {
	features, in, err := r.buildInputs(ctx, userID, topic, languages, tuning)
	if err != nil {
		return nil, err
	}

	// Which share of the page each video competes for. Recorded as scoring goes
	// because that is where subscription and affinity are already being weighed;
	// asking again afterwards would be asking the same questions twice and
	// risking a different answer.
	slots := make(map[string]feedSlot, len(features))
	ranked := make([]domain.RankedVideo, 0, len(features))

	for _, f := range features {
		breakdown := scoreVideo(f, in)
		if breakdown.Excluded != "" {
			continue
		}
		slots[f.VideoID] = breakdown.Slot
		ranked = append(ranked, domain.RankedVideo{
			VideoID: f.VideoID,
			Score:   breakdown.Score,
			Reason:  breakdown.Reason,
		})
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
	return applyChannelDiversity(applyDiscoveryQuota(ranked, slots, mix, in.tuning), channelOf,
		slots, maxPerChannelPerWindow, quotaWindow), nil
}

// MostWatched is the "played the most" collection, ordered by time spent.
func (r *Ranker) MostWatched(ctx context.Context, userID string, limit int32) ([]domain.RankedVideo, error) {
	return r.store.MostWatched(ctx, userID, limit)
}

func (r *Ranker) GetUpNext(ctx context.Context, userID, currentVideoID, channelFilter string, pageSize, pageToken int32) ([]domain.RankedVideo, int32, error) {
	features, err := r.features.ListVideoFeatures(ctx)
	if err != nil {
		return nil, 0, err
	}
	profile, err := r.store.BuildProfile(ctx, userID, impressionWindow)
	if err != nil {
		return nil, 0, err
	}

	watchAffinity := buildWatchAffinity(features, profile.WatchedFraction, profile.WatchedAt, time.Now())
	retention, err := r.store.VideoRetention(ctx)
	if err != nil {
		retention = map[string]float32{}
	}
	dislikes := buildDislikeAffinity(features, profile.Disliked, r.now())

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
		if _, disliked := profile.Disliked[f.VideoID]; disliked {
			continue
		}

		// Up-next deliberately ranks on the built-in constants. The advanced
		// settings answer "what should my home page look like"; the rail beside a
		// playing video is not that question, and a number moved on a settings
		// screen should not silently reorder it.
		score := weightRecentlyAdded * recencyBoost(f.AddedAt, now, recencyHalfLifeDay)
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
			score -= penaltyAlreadyWatched
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
		// Dislike affinity rides in the same damped group: it is general taste
		// too, and it must not outrank the relationship to the video actually
		// playing. Turning down some songs should not stop the rail following a
		// song with a song.
		score += upNextTasteDamping * (weightChannelAffinity*watchAffinity.Channels[f.ChannelID] +
			weightTopicAffinity*watchAffinity.TopicScore(f) +
			weightRetention*float64(retention[f.VideoID]) -
			weightDislikeAffinity*dislikes.Score(f))

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
	// Channel diversity only matters when showing all channels. When the
	// viewer explicitly picked "From {Channel}", there is only one channel
	// to show — capping it to 3 would hide the rest of its videos.
	paginated := ranked
	if channelFilter == "" {
		paginated = capPerChannel(ranked, channelOf, maxPerChannelUpNext, len(ranked))
	}

	// Paginate within the (possibly paginated) list.
	start := int(pageToken)
	if start >= len(paginated) {
		return nil, 0, nil
	}
	end := start + int(pageSize)
	if end > len(paginated) {
		end = len(paginated)
	}
	var nextToken int32
	if end < len(paginated) {
		nextToken = int32(end)
	}
	return paginated[start:end], nextToken, nil
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
// How long a viewing keeps its full say over what a channel is worth.
//
// Sixty days, against ninety for a dislike. Deliberately shorter: what somebody
// enjoyed fades faster than what they turned down, and a feed that keeps
// arguing from last spring's watching is the one this was written to fix.
const watchAffinityHalfLifeDays = 60.0

// watchTimeWeight turns seconds watched into a multiplier around 1.
//
// Logarithmic and centred so a few minutes is neutral: an hour is worth roughly
// twice a minute rather than sixty times, because what this reads is that time
// was given to something, not how much of it there was. A video with no known
// duration comes back neutral rather than zero — an unfilled column must not
// erase a real viewing.
func watchTimeWeight(seconds float64) float64 {
	if seconds <= 0 {
		return 1
	}
	return math.Log1p(seconds) / math.Log1p(180)
}

func buildWatchAffinity(
	features []domain.VideoFeatures, watched map[string]float32,
	watchedAt map[string]time.Time, now time.Time,
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
		// Partial views count less: skimming a channel builds far less
		// affinity than watching it to the end. Below half watched the
		// contribution is halved again — a hundred ten-second skims
		// should not outrank a few videos actually finished.
		weight := float64(fraction)
		if fraction < 0.5 {
			weight *= 0.25
		}

		// How long it actually took, not only how much of it there was.
		//
		// Fraction alone made half of a sixty-minute video and half of a
		// two-minute one the same evidence, when one is half an hour of
		// somebody's evening and the other is a minute. Logarithmic, so an hour
		// counts for more than a minute without counting for sixty times more —
		// what is being measured is that time was spent, not how much.
		weight *= watchTimeWeight(float64(fraction) * float64(feature.DurationSeconds))

		// And how long ago. Without this a channel watched to death last spring
		// argued exactly as loudly as one watched last night, for ever, which is
		// how a feed ends up frozen around whatever somebody used to like.
		if when, ok := watchedAt[videoID]; ok {
			weight *= halfLifeDecay(now.Sub(when), watchAffinityHalfLifeDays)
		}

		affinity.Channels[feature.ChannelID] += weight
		for _, topic := range feature.Topics {
			affinity.Topics[strings.ToLower(topic)] += weight
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
