package recommendation

import (
	"context"
	"math"
	"time"

	"recsys-ml/serving/internal/vectorstore"
)

// FeatureNames is the ranking feature order this service assembles.
//
// It must match `RANKING_FEATURES` in the training pipeline's config.py
// exactly. Nothing enforces that by construction across two languages, so the
// pipeline writes the order into feature_spec.json and the registry refuses to
// load a bundle that disagrees — see onnxmodel.FeatureSpec.Validate.
var FeatureNames = []string{
	"candidate_score",
	"completion_rate_avg",
	"hours_since_upload",
	"user_creator_affinity",
}

// Feature vector positions. Named rather than written as literals, because a
// transposed index here produces no error at all — just a model scoring the
// wrong columns and a feed that quietly gets worse.
const (
	featureCandidateScore = iota
	featureCompletionRateAvg
	featureHoursSinceUpload
	featureUserCreatorAffinity
	featureCount
)

// UserFeatures is everything about the viewer that ranking needs.
//
// Read from a store, never derived in the request path. Creator affinity is an
// average over that viewer's history; computing it per request would put a scan
// of their whole watch log inside a latency budget measured in milliseconds,
// and it changes far too slowly to be worth it.
type UserFeatures struct {
	// CreatorAffinity maps creator id to that viewer's average watch ratio for
	// them. A creator they have never watched is absent, not zero.
	CreatorAffinity map[string]float32
	// GlobalMeanWatchRatio is the fallback for an absent creator: the neutral
	// prior, rather than an assertion that they dislike the creator.
	GlobalMeanWatchRatio float32
}

// FeatureStore supplies the precomputed values ranking needs.
type FeatureStore interface {
	// UserFeatures returns features for one viewer. An unknown viewer is not an
	// error: everyone is unknown once, and a cold viewer must still get a feed.
	UserFeatures(ctx context.Context, userID string) (UserFeatures, error)
}

// affinityFor returns the viewer's affinity for a creator, or the neutral prior.
func (u UserFeatures) affinityFor(creatorID string) float32 {
	if affinity, ok := u.CreatorAffinity[creatorID]; ok {
		return affinity
	}
	return u.GlobalMeanWatchRatio
}

// hoursSinceUpload converts an upload timestamp to the freshness feature.
//
// An unknown upload time yields 0 rather than something enormous: zero reads as
// "brand new", which for a video whose age nobody recorded is the less damaging
// of the two guesses — it competes on its other features instead of being
// buried by an age it may not have.
func hoursSinceUpload(uploadedAtUnix int64, now time.Time) float32 {
	if uploadedAtUnix <= 0 {
		return 0
	}
	elapsed := now.Sub(time.Unix(uploadedAtUnix, 0)).Hours()
	if elapsed < 0 {
		// Clock skew, or an upload dated in the future. Clamp rather than feed
		// the model a negative age it never saw in training.
		return 0
	}
	return float32(elapsed)
}

// BuildFeatureRows turns candidates into the matrix the ranker scores.
//
// Row order matches the candidate slice exactly, so scores can be zipped back
// on to candidates by index.
func BuildFeatureRows(
	candidates []vectorstore.Candidate,
	user UserFeatures,
	now time.Time,
) [][]float32 {
	rows := make([][]float32, len(candidates))
	for i, candidate := range candidates {
		row := make([]float32, featureCount)
		row[featureCandidateScore] = sanitise(candidate.Score)
		row[featureCompletionRateAvg] = sanitise(candidate.CompletionRateAvg)
		row[featureHoursSinceUpload] = sanitise(hoursSinceUpload(candidate.UploadedAtUnix, now))
		row[featureUserCreatorAffinity] = sanitise(user.affinityFor(candidate.CreatorID))
		rows[i] = row
	}
	return rows
}

// sanitise replaces NaN and infinities with zero.
//
// LightGBM tolerates NaN — it learned a default direction for it — but the ONNX
// graph it converts to does not always, and an infinity propagates into a score
// that sorts above everything real. A single corrupt row would otherwise take
// the top of the feed.
func sanitise(value float32) float32 {
	if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
		return 0
	}
	return value
}
