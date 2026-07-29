// Package onnxmodel loads the artifacts the training pipeline exports and runs
// inference against them.
//
// The ONNX Runtime binding is behind the `onnx` build tag. Without the tag the
// package still compiles and every other package can be built and tested on a
// machine that has no ONNX Runtime installed — which is most development
// machines, and all of CI unless it is deliberately provisioned. A serving
// binary is built with `-tags onnx`; anything else gets a loader that fails
// cleanly, and a service whose fallback path is therefore exercised by default
// rather than only in an incident.
package onnxmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Artifact file names, fixed by the training pipeline.
const (
	VideoTowerFile  = "video_tower.onnx"
	RankerFile      = "ranker.onnx"
	FeatureSpecFile = "feature_spec.json"
	EmbeddingsFile  = "video_embeddings.parquet"
)

// SupportedSchemaVersion is the artifact contract this build understands. A
// bundle declaring anything else is refused rather than guessed at.
const SupportedSchemaVersion = 1

// ErrUnavailable reports that inference is not possible in this build or with
// these artifacts. Callers are expected to fall back rather than fail.
var ErrUnavailable = errors.New("onnxmodel: inference unavailable")

// Ranker scores candidates. Kept as an interface so the recommendation service
// can be tested without ONNX Runtime, and so a future ranker — a different
// framework, or a remote scorer — needs no change above it.
type Ranker interface {
	// Score returns one score per row. rows[i] must be ordered exactly as
	// FeatureSpec.RankingFeatures says.
	Score(ctx context.Context, rows [][]float32) ([]float32, error)
}

// FeatureSpec is the contract written by the training pipeline and validated
// here.
//
// It exists because training/serving skew is the failure mode that does not
// announce itself: swap two feature columns and the model keeps returning
// perfectly plausible numbers computed from the wrong inputs, and nothing
// crashes, and the recommendations quietly get worse. Comparing the order at
// load time turns a silent decay into a refusal to start.
type FeatureSpec struct {
	SchemaVersion   int      `json:"schema_version"`
	GeneratedAt     string   `json:"generated_at"`
	Label           string   `json:"label"`
	RankingFeatures []string `json:"ranking_features"`
	EmbeddingDim    int      `json:"embedding_dim"`
	Metrics         struct {
		RankerValidationRMSE     float64            `json:"ranker_validation_rmse"`
		TwoTowerValidationRecall float64            `json:"two_tower_validation_recall"`
		FeatureImportance        map[string]float64 `json:"feature_importance"`
	} `json:"metrics"`
}

// LoadFeatureSpec reads and validates the spec from an artifacts directory.
func LoadFeatureSpec(dir string) (*FeatureSpec, error) {
	raw, err := os.ReadFile(filepath.Join(dir, FeatureSpecFile))
	if err != nil {
		return nil, fmt.Errorf("onnxmodel: read feature spec: %w", err)
	}

	var spec FeatureSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("onnxmodel: parse feature spec: %w", err)
	}
	if spec.SchemaVersion != SupportedSchemaVersion {
		return nil, fmt.Errorf(
			"onnxmodel: artifact schema version %d, this build supports %d",
			spec.SchemaVersion, SupportedSchemaVersion,
		)
	}
	if len(spec.RankingFeatures) == 0 {
		return nil, errors.New("onnxmodel: feature spec lists no ranking features")
	}
	if spec.EmbeddingDim <= 0 {
		return nil, fmt.Errorf("onnxmodel: invalid embedding dimension %d", spec.EmbeddingDim)
	}
	return &spec, nil
}

// Validate checks a spec against the feature order this build assembles.
//
// Returns an error naming the first disagreement, because "features differ" is
// not actionable at three in the morning and "position 2 is hours_since_upload,
// expected completion_rate_avg" is.
func (s *FeatureSpec) Validate(expected []string) error {
	if len(s.RankingFeatures) != len(expected) {
		return fmt.Errorf(
			"onnxmodel: model expects %d ranking features, service builds %d",
			len(s.RankingFeatures), len(expected),
		)
	}
	for i := range expected {
		if s.RankingFeatures[i] != expected[i] {
			return fmt.Errorf(
				"onnxmodel: feature %d is %q in the model, %q in the service",
				i, s.RankingFeatures[i], expected[i],
			)
		}
	}
	return nil
}

// Bundle is one training run's artifacts, loaded and ready to serve.
type Bundle struct {
	Spec   *FeatureSpec
	Ranker Ranker
	// Fingerprint identifies the artifacts on disk this bundle was built from.
	// The registry compares it to decide whether a reload is warranted.
	Fingerprint string

	closer func() error
}

// Close releases the underlying inference sessions.
//
// Safe to call more than once: the registry may close a superseded bundle from
// its poller while a shutdown closes it from the other direction.
func (b *Bundle) Close() error {
	if b == nil || b.closer == nil {
		return nil
	}
	closer := b.closer
	b.closer = nil
	return closer()
}
