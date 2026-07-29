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

// Most videos from one channel allowed in a single window.
//
// Three of twenty-four is a visible presence without being the page. A viewer
// who watches one channel heavily should see it often; they should not have to
// scroll past it to find out the library contains anything else.
const maxPerChannelPerWindow = 3

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
	ranked []domain.RankedVideo, channelOf map[string]string,
) []domain.RankedVideo {
	if len(ranked) == 0 || len(channelOf) == 0 {
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
		if channel != "" && seen[channel] >= maxPerChannelPerWindow {
			return false
		}
		out = append(out, video)
		seen[channel]++
		inWindow++
		return true
	}

	for _, video := range ranked {
		if inWindow >= quotaWindow {
			startWindow()
			// Held-back videos outscore everything still to come, so they lead
			// the new window.
			remaining := deferred
			deferred = nil
			for _, held := range remaining {
				if !take(held) {
					deferred = append(deferred, held)
				}
				if inWindow >= quotaWindow {
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
