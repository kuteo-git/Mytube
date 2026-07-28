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
