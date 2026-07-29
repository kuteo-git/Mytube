package recommendation

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"recsys-ml/serving/internal/onnxmodel"
	"recsys-ml/serving/internal/vectorstore"
)

// --- test doubles ------------------------------------------------------------

// stubRanker stands in for the ONNX session so the whole flow is testable on a
// machine with no ONNX Runtime installed.
type stubRanker struct {
	scores  []float32
	err     error
	gotRows [][]float32
}

func (r *stubRanker) Score(_ context.Context, rows [][]float32) ([]float32, error) {
	r.gotRows = rows
	if r.err != nil {
		return nil, r.err
	}
	if r.scores != nil {
		return r.scores, nil
	}
	// Default: score by position so the ordering is predictable.
	scores := make([]float32, len(rows))
	for i := range rows {
		scores[i] = float32(len(rows) - i)
	}
	return scores, nil
}

type stubModels struct{ bundle *onnxmodel.Bundle }

func (m stubModels) Current() *onnxmodel.Bundle { return m.bundle }

type stubFeatures struct {
	features UserFeatures
	err      error
}

func (f stubFeatures) UserFeatures(context.Context, string) (UserFeatures, error) {
	return f.features, f.err
}

type stubTrending struct {
	ids []string
	err error
}

func (t stubTrending) Trending(_ context.Context, topN int) ([]string, error) {
	if t.err != nil {
		return nil, t.err
	}
	if len(t.ids) > topN {
		return t.ids[:topN], nil
	}
	return t.ids, nil
}

// failingStore reports an error for every operation.
type failingStore struct{ err error }

func (s failingStore) Search(context.Context, []float32, int) ([]vectorstore.Candidate, error) {
	return nil, s.err
}
func (s failingStore) Lookup(context.Context, []string) ([]vectorstore.Embedding, error) {
	return nil, s.err
}
func (s failingStore) Replace(context.Context, []vectorstore.Embedding) error { return s.err }
func (s failingStore) Len(context.Context) (int, error)                      { return 0, s.err }

// --- helpers -----------------------------------------------------------------

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// populatedStore builds an in-memory index of simple orthogonal-ish vectors.
func populatedStore(t *testing.T) *vectorstore.MemoryStore {
	t.Helper()
	store := vectorstore.NewMemoryStore()
	embeddings := []vectorstore.Embedding{
		{VideoID: "watched", Vector: []float32{1, 0, 0}, CreatorID: "a"},
		{VideoID: "near", Vector: []float32{0.9, 0.1, 0}, CreatorID: "a", CompletionRateAvg: 0.7},
		{VideoID: "mid", Vector: []float32{0.5, 0.5, 0}, CreatorID: "b", CompletionRateAvg: 0.5},
		{VideoID: "far", Vector: []float32{0, 0, 1}, CreatorID: "c", CompletionRateAvg: 0.2},
	}
	if err := store.Replace(context.Background(), embeddings); err != nil {
		t.Fatalf("seeding store: %v", err)
	}
	return store
}

func newTestService(t *testing.T, options Options) Service {
	t.Helper()
	if options.Logger == nil {
		options.Logger = quietLogger()
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	}
	service, err := New(options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return service
}

// --- tests -------------------------------------------------------------------

func TestNewRequiresAVectorStore(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected an error when no vector store is supplied")
	}
}

func TestRejectsNonPositiveTopN(t *testing.T) {
	service := newTestService(t, Options{Store: populatedStore(t)})
	if _, err := service.GetRecommendations(context.Background(), "u", nil, 0); err == nil {
		t.Fatal("expected an error for topN=0")
	}
}

func TestRankedPathScoresEveryCandidateAndSortsDescending(t *testing.T) {
	ranker := &stubRanker{scores: []float32{0.1, 0.9, 0.5}}
	service := newTestService(t, Options{
		Store:    populatedStore(t),
		Models:   stubModels{bundle: &onnxmodel.Bundle{Ranker: ranker}},
		Features: stubFeatures{features: UserFeatures{GlobalMeanWatchRatio: 0.4}},
	})

	results, err := service.GetRecommendations(
		context.Background(), "user_1", []string{"watched"}, 3,
	)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].Score < results[1].Score || results[1].Score < results[2].Score {
		t.Fatalf("results are not sorted descending: %+v", results)
	}
	for _, result := range results {
		if result.Source != SourceRanked {
			t.Fatalf("expected source %q, got %q", SourceRanked, result.Source)
		}
	}
	if len(ranker.gotRows) != 3 {
		t.Fatalf("ranker saw %d rows, expected 3", len(ranker.gotRows))
	}
	if len(ranker.gotRows[0]) != featureCount {
		t.Fatalf("ranker saw %d features, expected %d", len(ranker.gotRows[0]), featureCount)
	}
}

func TestAlreadyWatchedVideosAreNotRecommended(t *testing.T) {
	service := newTestService(t, Options{
		Store:  populatedStore(t),
		Models: stubModels{bundle: &onnxmodel.Bundle{Ranker: &stubRanker{}}},
	})

	results, err := service.GetRecommendations(
		context.Background(), "user_1", []string{"watched"}, 10,
	)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	for _, result := range results {
		if result.VideoID == "watched" {
			t.Fatal("a video already in the watch history was recommended back")
		}
	}
}

func TestFallsBackToTrendingWhenTheStoreFails(t *testing.T) {
	service := newTestService(t, Options{
		Store:    failingStore{err: errors.New("connection timed out")},
		Trending: stubTrending{ids: []string{"hot_1", "hot_2"}},
	})

	results, err := service.GetRecommendations(
		context.Background(), "user_1", []string{"watched"}, 5,
	)
	if err != nil {
		t.Fatalf("a store failure must not surface as an error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 trending results, got %d", len(results))
	}
	if results[0].Source != SourceTrending {
		t.Fatalf("expected source %q, got %q", SourceTrending, results[0].Source)
	}
}

func TestColdViewerWithNoHistoryGetsTrending(t *testing.T) {
	service := newTestService(t, Options{
		Store:    populatedStore(t),
		Models:   stubModels{bundle: &onnxmodel.Bundle{Ranker: &stubRanker{}}},
		Trending: stubTrending{ids: []string{"hot_1"}},
	})

	results, err := service.GetRecommendations(context.Background(), "new_user", nil, 5)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	if len(results) != 1 || results[0].Source != SourceTrending {
		t.Fatalf("expected the trending fallback, got %+v", results)
	}
}

func TestRankingFailureFallsBackToRetrievalOrderNotToTrending(t *testing.T) {
	// Retrieval already produced a relevance ordering. Throwing it away for
	// trending would be a much larger regression than serving it unranked.
	service := newTestService(t, Options{
		Store:    populatedStore(t),
		Models:   stubModels{bundle: &onnxmodel.Bundle{Ranker: &stubRanker{err: errors.New("boom")}}},
		Trending: stubTrending{ids: []string{"hot_1"}},
	})

	results, err := service.GetRecommendations(
		context.Background(), "user_1", []string{"watched"}, 5,
	)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected retrieval results")
	}
	if results[0].Source != SourceRetrieval {
		t.Fatalf("expected source %q, got %q", SourceRetrieval, results[0].Source)
	}
	// Nearest first.
	if results[0].VideoID != "near" {
		t.Fatalf("expected the nearest candidate first, got %q", results[0].VideoID)
	}
}

func TestMissingModelFallsBackToRetrievalOrder(t *testing.T) {
	service := newTestService(t, Options{
		Store:    populatedStore(t),
		Models:   stubModels{bundle: nil}, // no bundle loaded yet
		Trending: stubTrending{ids: []string{"hot_1"}},
	})

	results, err := service.GetRecommendations(
		context.Background(), "user_1", []string{"watched"}, 5,
	)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	if len(results) == 0 || results[0].Source != SourceRetrieval {
		t.Fatalf("expected retrieval results, got %+v", results)
	}
}

func TestUserFeatureFailureStillRanksOnTheNeutralPrior(t *testing.T) {
	ranker := &stubRanker{}
	service := newTestService(t, Options{
		Store:    populatedStore(t),
		Models:   stubModels{bundle: &onnxmodel.Bundle{Ranker: ranker}},
		Features: stubFeatures{err: errors.New("cache down")},
	})

	results, err := service.GetRecommendations(
		context.Background(), "user_1", []string{"watched"}, 5,
	)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	if len(results) == 0 || results[0].Source != SourceRanked {
		t.Fatalf("expected ranking to proceed, got %+v", results)
	}
}

func TestNoTrendingProviderReturnsEmptyRatherThanFailing(t *testing.T) {
	service := newTestService(t, Options{
		Store: failingStore{err: errors.New("down")},
	})

	results, err := service.GetRecommendations(
		context.Background(), "user_1", []string{"watched"}, 5,
	)
	if err != nil {
		t.Fatalf("expected a degraded answer, not an error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

func TestTopNIsRespected(t *testing.T) {
	service := newTestService(t, Options{
		Store:  populatedStore(t),
		Models: stubModels{bundle: &onnxmodel.Bundle{Ranker: &stubRanker{}}},
	})

	results, err := service.GetRecommendations(
		context.Background(), "user_1", []string{"watched"}, 2,
	)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}
