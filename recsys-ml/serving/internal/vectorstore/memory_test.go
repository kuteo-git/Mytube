package vectorstore

import (
	"context"
	"errors"
	"math"
	"testing"
)

func seeded(t *testing.T) *MemoryStore {
	t.Helper()
	store := NewMemoryStore()
	err := store.Replace(context.Background(), []Embedding{
		{VideoID: "a", Vector: []float32{1, 0, 0}},
		{VideoID: "b", Vector: []float32{0, 1, 0}},
		{VideoID: "c", Vector: []float32{0.7071, 0.7071, 0}},
	})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	return store
}

func TestSearchOnAnEmptyIndexIsDistinguishableFromNoMatches(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.Search(context.Background(), []float32{1, 0, 0}, 5)
	if !errors.Is(err, ErrEmptyIndex) {
		t.Fatalf("expected ErrEmptyIndex, got %v", err)
	}
}

func TestSearchOrdersByDescendingSimilarity(t *testing.T) {
	store := seeded(t)
	candidates, err := store.Search(context.Background(), []float32{1, 0, 0}, 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}
	if candidates[0].VideoID != "a" {
		t.Fatalf("expected the identical vector first, got %q", candidates[0].VideoID)
	}
	for i := 1; i < len(candidates); i++ {
		if candidates[i-1].Score < candidates[i].Score {
			t.Fatalf("candidates are not ordered: %+v", candidates)
		}
	}
}

func TestSearchRejectsAMismatchedQueryWidth(t *testing.T) {
	store := seeded(t)
	_, err := store.Search(context.Background(), []float32{1, 0}, 3)
	if !errors.Is(err, ErrDimensionMismatch) {
		t.Fatalf("expected ErrDimensionMismatch, got %v", err)
	}
}

func TestLookupSkipsUnknownIDsRatherThanFailing(t *testing.T) {
	// A video watched before the last training run indexed it is normal. Making
	// that an error would turn every new video into a source of failures.
	store := seeded(t)
	found, err := store.Lookup(context.Background(), []string{"a", "does_not_exist", "b"})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(found))
	}
}

func TestMeanVectorReturnsAUnitVector(t *testing.T) {
	mean, err := MeanVector([][]float32{{1, 0, 0}, {0, 1, 0}})
	if err != nil {
		t.Fatalf("MeanVector: %v", err)
	}
	var magnitude float64
	for _, value := range mean {
		magnitude += float64(value) * float64(value)
	}
	if math.Abs(math.Sqrt(magnitude)-1) > 1e-5 {
		t.Fatalf("expected a unit vector, got magnitude %v", math.Sqrt(magnitude))
	}
}

func TestMeanVectorOfNothingIsNilNotZero(t *testing.T) {
	// A zero vector is a real position in the space and would retrieve whatever
	// sits near the origin; nil forces the caller to handle "no history".
	mean, err := MeanVector(nil)
	if err != nil {
		t.Fatalf("MeanVector: %v", err)
	}
	if mean != nil {
		t.Fatalf("expected nil, got %v", mean)
	}
}

func TestMeanVectorOfOpposingVectorsIsNil(t *testing.T) {
	mean, err := MeanVector([][]float32{{1, 0}, {-1, 0}})
	if err != nil {
		t.Fatalf("MeanVector: %v", err)
	}
	if mean != nil {
		t.Fatalf("expected nil for a cancelling set, got %v", mean)
	}
}

func TestMeanVectorRejectsRaggedInput(t *testing.T) {
	if _, err := MeanVector([][]float32{{1, 0}, {1, 0, 0}}); !errors.Is(err, ErrDimensionMismatch) {
		t.Fatalf("expected ErrDimensionMismatch, got %v", err)
	}
}
