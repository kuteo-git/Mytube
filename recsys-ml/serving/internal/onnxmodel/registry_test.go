package onnxmodel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeArtifacts lays down a complete, plausible artifact set.
func writeArtifacts(t *testing.T, dir string, features []string) {
	t.Helper()

	spec := map[string]any{
		"schema_version":   SupportedSchemaVersion,
		// Nanosecond precision so two runs inside the same second still produce
		// distinct manifests, which is what the fingerprint keys on.
		"generated_at": time.Now().UTC().Format(time.RFC3339Nano),
		"label":            "watch_ratio",
		"ranking_features": features,
		"embedding_dim":    128,
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshalling spec: %v", err)
	}

	for name, content := range map[string][]byte{
		FeatureSpecFile: raw,
		RankerFile:      []byte("not a real onnx graph"),
		VideoTowerFile:  []byte("not a real onnx graph"),
		EmbeddingsFile:  []byte("not a real parquet file"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
}

func TestLoadFeatureSpecReadsAValidSpec(t *testing.T) {
	dir := t.TempDir()
	features := []string{"candidate_score", "completion_rate_avg"}
	writeArtifacts(t, dir, features)

	spec, err := LoadFeatureSpec(dir)
	if err != nil {
		t.Fatalf("LoadFeatureSpec: %v", err)
	}
	if spec.EmbeddingDim != 128 {
		t.Fatalf("embedding dim = %d, want 128", spec.EmbeddingDim)
	}
	if err := spec.Validate(features); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsReorderedFeatures(t *testing.T) {
	// The failure this guards against is silent: the model keeps returning
	// plausible numbers computed from the wrong columns.
	spec := &FeatureSpec{
		RankingFeatures: []string{"candidate_score", "completion_rate_avg", "hours_since_upload"},
	}
	err := spec.Validate(
		[]string{"candidate_score", "hours_since_upload", "completion_rate_avg"},
	)
	if err == nil {
		t.Fatal("expected reordered features to be rejected")
	}
	// The message has to name the position, or it is not actionable.
	if !strings.Contains(err.Error(), "feature 1") {
		t.Fatalf("error should identify the offending position, got: %v", err)
	}
}

func TestValidateRejectsDifferentFeatureCount(t *testing.T) {
	spec := &FeatureSpec{RankingFeatures: []string{"a", "b"}}
	if err := spec.Validate([]string{"a", "b", "c"}); err == nil {
		t.Fatal("expected a feature count mismatch to be rejected")
	}
}

func TestLoadFeatureSpecRejectsAnUnknownSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	writeArtifacts(t, dir, []string{"candidate_score"})

	raw, err := json.Marshal(map[string]any{
		"schema_version":   SupportedSchemaVersion + 1,
		"ranking_features": []string{"candidate_score"},
		"embedding_dim":    128,
	})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, FeatureSpecFile), raw, 0o600); err != nil {
		t.Fatalf("writing spec: %v", err)
	}

	if _, err := LoadFeatureSpec(dir); err == nil {
		t.Fatal("expected an unknown schema version to be rejected")
	}
}

func TestFingerprintIsStableAndChangesWithTheManifest(t *testing.T) {
	dir := t.TempDir()
	writeArtifacts(t, dir, []string{"candidate_score"})

	before, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	again, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if before != again {
		t.Fatal("fingerprint changed without the artifacts changing")
	}

	// A new run rewrites the manifest with a new generated_at.
	time.Sleep(time.Millisecond)
	writeArtifacts(t, dir, []string{"candidate_score"})

	after, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if before == after {
		t.Fatal("fingerprint did not change after a new manifest was published")
	}
}

func TestFingerprintIgnoresAHalfPublishedRun(t *testing.T) {
	// The regression this exists for: the two trainers are separate processes,
	// so between them the directory holds a new video tower beside an old
	// ranker. That state is stable across any number of polls, so a
	// "wait until it settles" guard alone would happily load the mismatched
	// pair. Keying the fingerprint on the manifest — written last — is what
	// makes the intermediate state invisible.
	dir := t.TempDir()
	writeArtifacts(t, dir, []string{"candidate_score"})

	before, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	// Stage one of a new run lands: tower and embeddings replaced, manifest not
	// yet rewritten.
	for _, name := range []string{VideoTowerFile, EmbeddingsFile} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("a freshly exported artifact"), 0o600); err != nil {
			t.Fatalf("rewriting %s: %v", name, err)
		}
	}

	during, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if during != before {
		t.Fatal("a half-published run changed the fingerprint and would have triggered a reload")
	}
}

func TestFingerprintFailsWhenAnArtifactIsMissing(t *testing.T) {
	dir := t.TempDir()
	writeArtifacts(t, dir, []string{"candidate_score"})
	if err := os.Remove(filepath.Join(dir, EmbeddingsFile)); err != nil {
		t.Fatalf("removing embeddings: %v", err)
	}

	if _, err := Fingerprint(dir); err == nil {
		t.Fatal("expected a missing artifact to fail fingerprinting")
	}
}

func TestPollWaitsForTheFingerprintToSettleBeforeLoading(t *testing.T) {
	// A training run writes four files. Loading on the first change would open
	// a new ranker beside old embeddings, whose vector spaces are unrelated.
	dir := t.TempDir()
	writeArtifacts(t, dir, []string{"candidate_score"})

	registry := NewRegistry(RegistryOptions{
		Dir:              dir,
		ExpectedFeatures: []string{"candidate_score"},
	})

	registry.poll()
	registry.mu.Lock()
	firstSeen := registry.pendingSince
	loadedAfterOnePoll := registry.lastLoadedPrint
	registry.mu.Unlock()

	if firstSeen != 1 {
		t.Fatalf("expected the change to be pending after one poll, got %d", firstSeen)
	}
	if loadedAfterOnePoll != "" {
		t.Fatal("a single poll must not trigger a load")
	}

	// Second poll sees the same fingerprint: settled, so a load is attempted.
	// It fails here because the stand-in files are not real ONNX graphs — which
	// is itself the behaviour under test: a bad bundle must not take the
	// service down.
	registry.poll()
	if registry.Current() != nil {
		t.Fatal("a bundle built from invalid artifacts must not become current")
	}
}

func TestPollDoesNotRetryTheSameBrokenBundleForever(t *testing.T) {
	dir := t.TempDir()
	writeArtifacts(t, dir, []string{"candidate_score"})

	registry := NewRegistry(RegistryOptions{
		Dir:              dir,
		ExpectedFeatures: []string{"candidate_score"},
	})

	registry.poll() // pending
	registry.poll() // settled, load attempted and fails
	registry.poll() // must be a no-op now

	registry.mu.Lock()
	failures := registry.consecutiveFails
	registry.mu.Unlock()

	if failures != 1 {
		t.Fatalf("expected exactly one load attempt for a broken bundle, got %d", failures)
	}
}
