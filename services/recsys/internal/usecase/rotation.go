package usecase

import (
	"math"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// Why Home was the same four channels.
//
// Measured on this library before any of it existed: three channels held 38 of
// the first 120 slots, and 55 of the 85 subscribed channels had no video before
// position 240. Almost nothing was excluded — two channels, for being over a
// year old. Everything else was simply ranked past where anyone scrolls.
//
// The cause was not the buckets, which are broad: the affinity bucket holds 174
// videos across 106 channels. It is that sampling only sees the best
// `samplePoolSize` of a bucket, and the same channels were at the top of that
// pool on every page, on every request, for as long as the affinity that put
// them there kept accumulating. Nothing in the score had any notion of time
// passing, of a channel going quiet, or of an offer already declined.
//
// So three things age here, on three different clocks, because they answer
// three different questions:
//
//	heat     — have I been served this channel lately?   half-life 7 days
//	revival  — how long since I last watched it?         saturates over ~3 weeks
//	ignored  — how often was this offered and refused?   no clock, a count
//
// Taste itself is elsewhere and moves slowly. These only decide which of the
// things a viewer likes gets to be on today's page.

// How long a channel stays "hot" after it has been watched or shown.
//
// A week, chosen by the person this feed is for. It is short on purpose: the
// complaint was not that the wrong channels were being recommended, it was that
// the right ones were being recommended without pause. Halving every seven days
// means a channel binged on Monday is at a quarter strength by the middle of the
// month and out of the way, without ever being marked down as a channel the
// viewer dislikes.
const channelHeatHalfLifeDays = 7.0

// How much a hot channel is pushed down.
//
// Comparable to weightSubscribed (2.5) rather than to the affinity multiplier,
// because it has to be able to move a channel out of the top of a sample pool
// without being able to bury it. A channel at full heat drops by roughly what
// subscribing to it lifted.
const weightChannelHeat = 2.0

// How long it takes a channel to come back after being left alone.
//
// Three weeks to saturate, so this is slower than heat decays. The asymmetry is
// deliberate: falling quiet should be quick and returning should be gradual, or
// the feed swaps one rut for a rotation just as predictable.
const channelRevivalDays = 21.0

// How far a forgotten channel can be lifted.
//
// Small — half of what subscribing gives — and it only ever applies to a
// channel the viewer already has affinity for. A channel nobody ever watched is
// not owed a revival; that is what the discovery share is for.
const weightChannelRevival = 1.2

// Offers before an unopened video is taken as declined.
//
// Three, because one is noise and two is a coincidence. Below this the count
// says nothing: a video can be on screen without being seen at all.
const ignoredImpressionFloor = 3

// How hard a declined offer is pushed down, per impression past the floor.
//
// Cumulative and capped. A thumbnail that has been in front of somebody eight
// times without ever being pressed is not a thumbnail that needs a ninth
// showing, whatever its topic score says.
const weightIgnoredImpression = 0.5
const maxIgnoredPenalty = 3.0

// ChannelRotation is the per-channel state of "shown lately" and "gone quiet".
//
// Built once per request from the profile, like every other signal here: there
// is no model and nothing stored, so the effect of watching something is
// visible on the next grid and the reason can always be printed.
type ChannelRotation struct {
	// 0..1, how recently and how much this channel has been watched or shown.
	Heat map[string]float64
	// When each channel was last watched. Absent means never.
	LastWatched map[string]time.Time
}

func buildChannelRotation(
	features []domain.VideoFeatures,
	profile domain.UserProfile,
	now time.Time,
) ChannelRotation {
	out := ChannelRotation{
		Heat:        map[string]float64{},
		LastWatched: map[string]time.Time{},
	}

	byID := make(map[string]domain.VideoFeatures, len(features))
	for _, f := range features {
		byID[f.VideoID] = f
	}

	for videoID, when := range profile.WatchedAt {
		f, ok := byID[videoID]
		if !ok {
			continue
		}
		// A watch counts for the channel in proportion to how much of it was
		// watched: a bounce is not a sitting, and should not make a channel hot
		// enough to be rested.
		weight := float64(profile.WatchedFraction[videoID])
		out.Heat[f.ChannelID] += weight * halfLifeDecay(now.Sub(when), channelHeatHalfLifeDays)

		if last, seen := out.LastWatched[f.ChannelID]; !seen || when.After(last) {
			out.LastWatched[f.ChannelID] = when
		}
	}

	// Being shown counts too, at a fraction of being watched.
	//
	// Without this a channel offered on every page and never opened stays cold
	// for ever and keeps being offered — which is most of what "the feed is
	// boring" describes. It is worth less than a watch because an impression is
	// something the feed did, not something the viewer did.
	for videoID := range profile.RecentImpressions {
		if f, ok := byID[videoID]; ok {
			out.Heat[f.ChannelID] += 0.25
		}
	}

	normaliseToUnit(out.Heat)
	return out
}

// FatigueFor is how far this channel should stand down today, 0..1.
func (r ChannelRotation) FatigueFor(channelID string) float64 {
	return r.Heat[channelID]
}

// RevivalFor is how much a channel is owed for having been left alone, 0..1.
//
// Zero for a channel never watched: this lifts things back out of the past, and
// something never seen has no past here. Zero also for one watched today, which
// is the whole point of it.
func (r ChannelRotation) RevivalFor(channelID string, now time.Time) float64 {
	last, ok := r.LastWatched[channelID]
	if !ok {
		return 0
	}
	days := now.Sub(last).Hours() / 24
	if days <= 0 {
		return 0
	}
	if days >= channelRevivalDays {
		return 1
	}
	return days / channelRevivalDays
}

// ignoredPenalty is what repeatedly declining a video costs it.
//
// The viewer saw the thumbnail, the title and the channel, and chose something
// else — several times. That is a real answer about this particular video, and
// it is the only signal here that reads a thumbnail at all, since nothing in
// this service has ever seen one.
//
// Watched videos are exempt outright rather than merely scoring differently: a
// video someone opened has not been declined, however often it was offered
// first.
func ignoredPenalty(videoID string, profile domain.UserProfile) float64 {
	if _, watched := profile.WatchedFraction[videoID]; watched {
		return 0
	}
	shown := profile.ImpressionCounts[videoID]
	if shown <= ignoredImpressionFloor {
		return 0
	}
	penalty := weightIgnoredImpression * float64(shown-ignoredImpressionFloor)
	return math.Min(penalty, maxIgnoredPenalty)
}

// halfLifeDecay is 1 at zero age and halves every halfLifeDays.
func halfLifeDecay(age time.Duration, halfLifeDays float64) float64 {
	days := age.Hours() / 24
	if days <= 0 {
		return 1
	}
	return math.Pow(0.5, days/halfLifeDays)
}
