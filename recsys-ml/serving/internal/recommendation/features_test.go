package recommendation

import (
	"math"
	"testing"
	"time"

	"recsys-ml/serving/internal/vectorstore"
)

func TestFeatureNamesMatchVectorWidth(t *testing.T) {
	// The named indices and the exported order are edited in different places;
	// this is what catches one being updated without the other.
	if len(FeatureNames) != featureCount {
		t.Fatalf("FeatureNames has %d entries, feature vector has %d slots",
			len(FeatureNames), featureCount)
	}
}

func TestBuildFeatureRowsPlacesEachFeatureAtItsIndex(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	uploaded := now.Add(-48 * time.Hour)

	candidates := []vectorstore.Candidate{{
		Embedding: vectorstore.Embedding{
			VideoID:           "vid_1",
			CompletionRateAvg: 0.62,
			UploadedAtUnix:    uploaded.Unix(),
			CreatorID:         "creator_a",
		},
		Score: 0.91,
	}}
	user := UserFeatures{
		CreatorAffinity:      map[string]float32{"creator_a": 0.77},
		GlobalMeanWatchRatio: 0.4,
	}

	rows := BuildFeatureRows(candidates, user, now)

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if len(row) != featureCount {
		t.Fatalf("expected %d features, got %d", featureCount, len(row))
	}
	assertClose(t, "candidate_score", row[featureCandidateScore], 0.91)
	assertClose(t, "completion_rate_avg", row[featureCompletionRateAvg], 0.62)
	assertClose(t, "hours_since_upload", row[featureHoursSinceUpload], 48)
	assertClose(t, "user_creator_affinity", row[featureUserCreatorAffinity], 0.77)
}

func TestUnknownCreatorFallsBackToGlobalMeanNotZero(t *testing.T) {
	// Zero would assert the viewer dislikes a creator they have simply never
	// encountered, which pushes every new creator to the bottom of every feed.
	user := UserFeatures{
		CreatorAffinity:      map[string]float32{"creator_a": 0.9},
		GlobalMeanWatchRatio: 0.35,
	}
	candidates := []vectorstore.Candidate{{
		Embedding: vectorstore.Embedding{VideoID: "v", CreatorID: "never_seen"},
	}}

	rows := BuildFeatureRows(candidates, user, time.Unix(1_800_000_000, 0))
	assertClose(t, "affinity fallback", rows[0][featureUserCreatorAffinity], 0.35)
}

func TestHoursSinceUpload(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)

	tests := []struct {
		name     string
		uploaded int64
		want     float32
	}{
		{"unknown upload time is treated as fresh", 0, 0},
		{"negative timestamp is treated as fresh", -5, 0},
		{"future upload is clamped, not negative", now.Add(time.Hour).Unix(), 0},
		{"three hours ago", now.Add(-3 * time.Hour).Unix(), 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hoursSinceUpload(test.uploaded, now); math.Abs(float64(got-test.want)) > 1e-3 {
				t.Fatalf("hoursSinceUpload(%d) = %v, want %v", test.uploaded, got, test.want)
			}
		})
	}
}

func TestSanitiseRemovesNaNAndInfinity(t *testing.T) {
	// An infinity sorts above every real score and would take the top of the
	// feed on its own.
	for _, value := range []float32{
		float32(math.NaN()),
		float32(math.Inf(1)),
		float32(math.Inf(-1)),
	} {
		if got := sanitise(value); got != 0 {
			t.Fatalf("sanitise(%v) = %v, want 0", value, got)
		}
	}
	if got := sanitise(0.5); got != 0.5 {
		t.Fatalf("sanitise(0.5) = %v, want 0.5", got)
	}
}

func assertClose(t *testing.T, name string, got, want float32) {
	t.Helper()
	if math.Abs(float64(got-want)) > 1e-4 {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}
