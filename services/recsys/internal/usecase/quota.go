package usecase

import (
	"math/rand"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// Which share of the feed a video competes for.
//
// Deliberately not the same thing as domain.Reason. A reason answers "why is
// this here" and there are nine of them, several of which can be true at once;
// a slot answers "whose share of the page does this take", and every video has
// exactly one. Keeping them separate is what lets the viewer be given three
// sliders they can hold in their head — subscribed, more of what I like,
// something new — without the feed losing the finer-grained reason it reports
// per video.
type feedSlot int

const (
	slotOther feedSlot = iota
	slotContinueWatching
	slotRewatch
	slotSubscribed
	slotAffinity
	slotDiscovery
)

// The two shares the viewer cannot adjust.
//
// Finishing something already started and going back to something already
// finished are states of the watch history, not sources of new material. The
// setting is about where new material comes from, and making these compete for
// it would mean a viewer who wants more discovery is asked to give up the video
// they were halfway through.
const (
	shareContinueWatching = 0.10
	shareRewatch          = 0.08
	// What is left for the three the viewer does control.
	shareAdjustable = 1 - shareContinueWatching - shareRewatch
)

// FeedMix is how the adjustable share is divided, in percent.
//
// This exists because scoring alone does not stay open. Likes and subscriptions
// both push the feed toward what is already familiar, and over a library of a
// few hundred videos that convergence happens within a few dozen likes — far
// faster than it would on a catalogue of millions. The quota reserves room for
// material the score would otherwise bury; the mix decides how much.
//
// It reorders and never drops: everything ranked is still reachable by
// scrolling, just not in pure score order.
type FeedMix struct {
	Subscribed int
	Affinity   int
	Discovery  int
}

// DefaultFeedMix reproduces the fixed quota this replaced.
//
// The old buckets were NeverWatched 30, Subscribed 20, RecentlyAdded 15,
// Discovery 12 — and the first and third are both "not subscribed, matches what
// they watch", which is the affinity share. Normalised over the 82% left after
// the two fixed shares, that is 25/60/15. Chosen so that installing this
// changes nothing until somebody moves a slider: if the feed shifted on upgrade
// there would be no way to tell a setting working from a default changing.
var DefaultFeedMix = FeedMix{Subscribed: 25, Affinity: 60, Discovery: 15}

// normalised turns whatever the caller sent into shares of the adjustable part.
//
// Percentages that do not add to a hundred are honoured by ratio rather than
// rejected: 3/2/1 and 50/33/17 describe the same feed, and a service is not the
// place to argue about arithmetic the UI already does.
func (m FeedMix) normalised() (subscribed, affinity, discovery float64) {
	total := m.Subscribed + m.Affinity + m.Discovery
	if total <= 0 {
		m = DefaultFeedMix
		total = m.Subscribed + m.Affinity + m.Discovery
	}
	scale := shareAdjustable / float64(total)
	return float64(m.Subscribed) * scale,
		float64(m.Affinity) * scale,
		float64(m.Discovery) * scale
}

// quotaWindow is the span the ratios apply over. Matching the default page size
// means the mix is visible on the first screen rather than emerging over
// several.
const quotaWindow = 24

type quotaBucket struct {
	slot  feedSlot
	share float64
	// Whether an empty share still gets one place per window.
	//
	// The floor exists so a thinly-stocked bucket is not starved, which is a
	// good rule for a share nobody chose and a bad one for a share somebody set
	// to zero on purpose: a slider dragged to the bottom that still produces
	// videos is a control that lies about what it does.
	floor bool
}

func bucketsFor(mix FeedMix) []quotaBucket {
	subscribed, affinity, discovery := mix.normalised()
	return []quotaBucket{
		{slot: slotAffinity, share: affinity},
		{slot: slotSubscribed, share: subscribed},
		{slot: slotDiscovery, share: discovery},
		{slot: slotContinueWatching, share: shareContinueWatching, floor: true},
		{slot: slotRewatch, share: shareRewatch, floor: true},
	}
}

// applyDiscoveryQuota interleaves the ranking into the requested mix.
//
// The slot each video belongs to is passed in rather than read off the video:
// it is a decision the ranker already made while scoring, and domain.RankedVideo
// crosses the wire to the gateway, which has no use for it.
func applyDiscoveryQuota(
	ranked []domain.RankedVideo,
	slots map[string]feedSlot,
	mix FeedMix,
) []domain.RankedVideo {
	if len(ranked) == 0 {
		return ranked
	}
	buckets := bucketsFor(mix)

	// Split by slot, preserving the score order within each bucket.
	bySlot := make(map[feedSlot][]domain.RankedVideo, len(buckets))
	var other []domain.RankedVideo
	for _, v := range ranked {
		slot, ok := slots[v.VideoID]
		if !ok || slot == slotOther {
			other = append(other, v)
			continue
		}
		bySlot[slot] = append(bySlot[slot], v)
	}

	// Shuffle each bucket so the feed looks different on each refresh.
	// Seeded by the minute so it changes often enough to feel dynamic without
	// flipping every request.
	rng := rand.New(rand.NewSource(time.Now().Truncate(time.Minute).UnixNano()))
	for _, bucket := range buckets {
		shuffleSlice(bySlot[bucket.slot], rng)
	}
	shuffleSlice(other, rng)

	// A share of zero is an instruction, not a shortage: those videos are held
	// back entirely rather than filling gaps later, which is the difference
	// between "show me less of this" and "show me none of this".
	var suppressed []domain.RankedVideo
	for _, bucket := range buckets {
		if bucket.share <= 0 && !bucket.floor {
			suppressed = append(suppressed, bySlot[bucket.slot]...)
			bySlot[bucket.slot] = nil
		}
	}

	out := make([]domain.RankedVideo, 0, len(ranked))
	for len(out) < len(ranked)-len(suppressed) {
		before := len(out)

		for _, bucket := range buckets {
			take := int(bucket.share * quotaWindow)
			if take < 1 && bucket.floor {
				take = 1
			}
			available := bySlot[bucket.slot]
			if take > len(available) {
				take = len(available)
			}
			if take <= 0 {
				continue
			}
			out = append(out, available[:take]...)
			bySlot[bucket.slot] = available[take:]
		}

		// Buckets can empty at different rates. Whatever is left over — slots
		// outside the quota, or the remainder of an over-full bucket — fills the
		// gap in score order rather than leaving the page short.
		if len(out) == before {
			for _, bucket := range buckets {
				out = append(out, bySlot[bucket.slot]...)
				bySlot[bucket.slot] = nil
			}
			out = append(out, other...)
			other = nil
			break
		}
	}

	// Anything the loop did not reach.
	for _, bucket := range buckets {
		out = append(out, bySlot[bucket.slot]...)
	}
	out = append(out, other...)
	// Last, and only so nothing becomes unreachable by scrolling — the quota
	// has never dropped a video and does not start here.
	return append(out, suppressed...)
}

// Most videos from one channel allowed in a single window of the feed.
//
// Three of twenty-four is a visible presence without being the page. A viewer
// who watches one channel heavily should see it often; they should not have to
// scroll past it to find out the library contains anything else.
const maxPerChannelPerWindow = 3

// The same limit for the up-next rail, which is twenty long.
//
// Relatedness ranks the channel of the video playing above everything else, so
// without a cap the rail is that one channel and nothing else — measured at
// 20 of 20. Capping it keeps the rail on topic while letting other channels
// covering the same subject in: this library holds 140 Entertainment videos
// across 26 channels and 218 Music videos across 35, so there is no shortage
// of material that is related without being identical.
const maxPerChannelUpNext = 3

// capPerChannel takes the best-scoring videos subject to a hard per-channel
// limit, and stops.
//
// Different from applyChannelDiversity, which reorders an entire ranking and
// puts what it held back at the end. That is right for a feed, which is scrolled
// and where nothing may become unreachable. It is wrong for a rail of twenty:
// the held-back videos land inside the first twenty anyway, and the channel
// being limited quietly takes eight of the slots instead of three.
//
// Here the limit is absolute. A rail shorter than requested is an honest
// statement that the library holds little else on the subject — and on this
// library it rarely happens, since Entertainment spans 26 channels and Music 35.
func capPerChannel(
	ranked []domain.RankedVideo, channelOf map[string]string, perChannel, limit int,
) []domain.RankedVideo {
	if perChannel <= 0 || limit <= 0 {
		return ranked
	}

	out := make([]domain.RankedVideo, 0, limit)
	seen := map[string]int{}
	for _, video := range ranked {
		if len(out) >= limit {
			break
		}
		channel := channelOf[video.VideoID]
		// An unknown channel cannot crowd anything out, so it is never limited.
		if channel != "" {
			if seen[channel] >= perChannel {
				continue
			}
			seen[channel]++
		}
		out = append(out, video)
	}
	return out
}

// applyChannelDiversity limits how much of any one page a single channel can
// occupy.
//
// The reason quota above cannot do this, and the difference is worth stating
// because it looked for a long time as though it could. That quota mixes by
// *why* a video was chosen. A channel a viewer watches heavily produces videos
// that are simultaneously never-watched, recently-added and from a subscribed
// channel — so it fills every bucket at once and the mix stays satisfied while
// the page shows one thing. Measured on this library: 44% of all watch time
// went to a single channel, its affinity normalised to 1.0 against a runner-up
// at 0.23, and the resulting front page was 23 of 24 videos from that one
// channel while every quota was nominally being met.
//
// Like the reason quota this reorders and never drops: a video pushed out of
// one window appears in the next, so nothing becomes unreachable by scrolling.
func applyChannelDiversity(
	ranked []domain.RankedVideo, channelOf map[string]string, perChannel, window int,
) []domain.RankedVideo {
	if len(ranked) == 0 || len(channelOf) == 0 || perChannel <= 0 || window <= 0 {
		return ranked
	}

	out := make([]domain.RankedVideo, 0, len(ranked))
	// Videos held back from the current window, still in score order.
	var deferred []domain.RankedVideo
	seen := map[string]int{}
	inWindow := 0

	startWindow := func() {
		seen = map[string]int{}
		inWindow = 0
	}

	take := func(video domain.RankedVideo) bool {
		channel := channelOf[video.VideoID]
		// A video whose channel is unknown cannot crowd a channel out, so it is
		// never held back.
		if channel != "" && seen[channel] >= perChannel {
			return false
		}
		out = append(out, video)
		seen[channel]++
		inWindow++
		return true
	}

	for _, video := range ranked {
		if inWindow >= window {
			startWindow()
			// Held-back videos outscore everything still to come, so they lead
			// the new window.
			remaining := deferred
			deferred = nil
			for _, held := range remaining {
				if !take(held) {
					deferred = append(deferred, held)
				}
				if inWindow >= window {
					break
				}
			}
		}
		if !take(video) {
			deferred = append(deferred, video)
		}
	}

	// Whatever is still held back goes on the end, in score order. Emitting it
	// under the cap would need windows nobody is going to scroll to.
	return append(out, deferred...)
}

// shuffleSlice randomly reorders a slice using the provided source.
func shuffleSlice(s []domain.RankedVideo, rng *rand.Rand) {
	rng.Shuffle(len(s), func(i, j int) {
		s[i], s[j] = s[j], s[i]
	})
}
