package usecase

import "time"

// Tuning is the handful of ranking constants the household can move.
//
// Every field is a pointer so that "not set" and "set to zero" stay different
// things. Several of these mean something at zero — a session blend of zero is a
// viewer saying to ignore what they are watching right now — and a struct of
// bare values could not tell that from a caller who filled in nothing.
//
// The set is deliberately small. It is the settings that answer a question
// somebody has, not the two dozen score weights, which answer questions nobody
// asks and whose only defence is that each one can be explained.
type Tuning struct {
	SessionBlend           *float64
	FreshSubscribedPercent *int
	FreshnessWindowHours   *int
	MaxPublishedAgeDays    *int
	RecencyHalfLifeDays    *float64
	SoftmaxTemperature     *float64
	SamplePoolSize         *int
}

// resolvedTuning is what the ranker actually reads: every value present, every
// value inside its safe range.
type resolvedTuning struct {
	sessionBlend         float64
	shareFreshSubscribed float64
	freshnessWindow      time.Duration
	maxPublishedAgeDays  float64
	recencyHalfLifeDay   float64
	softmaxTemperature   float64
	samplePoolSize       int
}

// The bounds each setting is held inside.
//
// Clamped rather than rejected, which is the posture the gateway's feed mix
// already takes: this decides the order of a grid, and there is no reading of
// "the feed will not load" that beats "the feed is not quite what you asked
// for". The file is meant to be edited by hand, so a typo of 10000 has to be
// survivable.
//
// Two of these are guarding against a specific, measured failure rather than
// against nonsense. sample_pool_size is the one that matters: Gumbel noise grows
// like log N, so entering the whole of a several-thousand-video bucket into the
// draw put eight videos scoring below zero on the first page of twenty-four
// while forty above fourteen went unused. The ceiling here is well short of
// where that starts. The temperature floor is the other half — at zero the feed
// is frozen in score order and looks identical on every refresh, which is the
// bug the sampling replaced.
var tuningBounds = struct {
	sessionBlend           [2]float64
	freshSubscribedPercent [2]int
	freshnessWindowHours   [2]int
	maxPublishedAgeDays    [2]int
	recencyHalfLifeDays    [2]float64
	softmaxTemperature     [2]float64
	samplePoolSize         [2]int
}{
	sessionBlend:           [2]float64{0, 1},
	freshSubscribedPercent: [2]int{0, 40},
	freshnessWindowHours:   [2]int{1, 24 * 14},
	// A feed that only shows today is a defensible choice; a feed that shows
	// nothing is not, which is why the floor is a day rather than zero.
	maxPublishedAgeDays: [2]int{1, 3650},
	recencyHalfLifeDays: [2]float64{0.5, 365},
	softmaxTemperature:  [2]float64{0.05, 5},
	samplePoolSize:      [2]int{quotaWindow, 20 * quotaWindow},
}

// defaultTuning is the built-in behaviour, and the answer whenever a setting is
// absent. The values are the constants the ranker used before any of this
// existed, so an installation that never opens the advanced settings ranks
// exactly as it did.
func defaultTuning() resolvedTuning {
	return resolvedTuning{
		sessionBlend:         sessionBlend,
		shareFreshSubscribed: shareFreshSubscribed,
		freshnessWindow:      freshnessWindow,
		maxPublishedAgeDays:  maxPublishedAgeDays,
		recencyHalfLifeDay:   recencyHalfLifeDay,
		softmaxTemperature:   softmaxTemperature,
		samplePoolSize:       samplePoolSize,
	}
}

func (t Tuning) resolve() resolvedTuning {
	out := defaultTuning()
	if t.SessionBlend != nil {
		out.sessionBlend = clampFloat(*t.SessionBlend, tuningBounds.sessionBlend)
	}
	if t.FreshSubscribedPercent != nil {
		out.shareFreshSubscribed =
			float64(clampInt(*t.FreshSubscribedPercent, tuningBounds.freshSubscribedPercent)) / 100
	}
	if t.FreshnessWindowHours != nil {
		out.freshnessWindow = time.Duration(
			clampInt(*t.FreshnessWindowHours, tuningBounds.freshnessWindowHours)) * time.Hour
	}
	if t.MaxPublishedAgeDays != nil {
		out.maxPublishedAgeDays = float64(
			clampInt(*t.MaxPublishedAgeDays, tuningBounds.maxPublishedAgeDays))
	}
	if t.RecencyHalfLifeDays != nil {
		out.recencyHalfLifeDay = clampFloat(*t.RecencyHalfLifeDays, tuningBounds.recencyHalfLifeDays)
	}
	if t.SoftmaxTemperature != nil {
		out.softmaxTemperature = clampFloat(*t.SoftmaxTemperature, tuningBounds.softmaxTemperature)
	}
	if t.SamplePoolSize != nil {
		out.samplePoolSize = clampInt(*t.SamplePoolSize, tuningBounds.samplePoolSize)
	}
	return out
}

func clampFloat(v float64, bounds [2]float64) float64 {
	// A NaN compares false against everything, so it would pass both bounds and
	// then poison every score it touched. JSON cannot carry one, but the field
	// is a double on the wire and this is cheaper than finding out.
	if v != v {
		return bounds[0]
	}
	if v < bounds[0] {
		return bounds[0]
	}
	if v > bounds[1] {
		return bounds[1]
	}
	return v
}

func clampInt(v int, bounds [2]int) int {
	if v < bounds[0] {
		return bounds[0]
	}
	if v > bounds[1] {
		return bounds[1]
	}
	return v
}

// TuningBounds reports the valid range of each setting, for the UI to draw its
// sliders against.
//
// Published rather than duplicated in the browser for the same reason the feed
// mix's defaults are sent down: a slider whose ends disagree with the server's
// clamp is a control that silently does nothing at one end.
func TuningBounds() map[string][2]float64 {
	return map[string][2]float64{
		"sessionBlend":           tuningBounds.sessionBlend,
		"freshSubscribedPercent": {float64(tuningBounds.freshSubscribedPercent[0]), float64(tuningBounds.freshSubscribedPercent[1])},
		"freshnessWindowHours":   {float64(tuningBounds.freshnessWindowHours[0]), float64(tuningBounds.freshnessWindowHours[1])},
		"maxPublishedAgeDays":    {float64(tuningBounds.maxPublishedAgeDays[0]), float64(tuningBounds.maxPublishedAgeDays[1])},
		"recencyHalfLifeDays":    tuningBounds.recencyHalfLifeDays,
		"softmaxTemperature":     tuningBounds.softmaxTemperature,
		"samplePoolSize":         {float64(tuningBounds.samplePoolSize[0]), float64(tuningBounds.samplePoolSize[1])},
	}
}
