// Package recommendation joins candidate generation to ranking.
//
// The two stages stay separate all the way down, and that is the whole design.
// Retrieval must consider the entire catalogue, so it is restricted to a dot
// product against precomputed vectors. Ranking sees a few hundred survivors and
// can afford features retrieval cannot express — freshness, how well a video
// holds an audience, how much this viewer likes this creator. Merging them
// would force the expensive features onto every video in the catalogue, or
// discard them.
package recommendation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"recsys-ml/serving/internal/onnxmodel"
	"recsys-ml/serving/internal/vectorstore"
)

// VideoScore is one recommendation.
type VideoScore struct {
	VideoID string  `json:"videoId"`
	Score   float32 `json:"score"`
	// Source records which path produced this result. Worth returning rather
	// than hiding: a feed silently served by the fallback for a week looks
	// exactly like a working recommender from the outside.
	Source Source `json:"source"`
}

// Source names the path that produced a result.
type Source string

const (
	// SourceRanked means retrieval and the ranking model both ran.
	SourceRanked Source = "ranked"
	// SourceRetrieval means retrieval ran but ranking did not, so results are
	// ordered by similarity alone.
	SourceRetrieval Source = "retrieval"
	// SourceTrending means retrieval was unavailable and the fallback answered.
	SourceTrending Source = "trending"
)

// Service is the recommendation entry point.
type Service interface {
	// GetRecommendations returns at most topN videos for a viewer.
	//
	// It does not return an error for any condition the service can degrade
	// through: a missing model, an empty index, a slow vector store. Those
	// produce a lower-quality feed and a log line. An error means the request
	// itself was unanswerable.
	GetRecommendations(
		ctx context.Context, userID string, watchHistory []string, topN int,
	) ([]VideoScore, error)
}

// TrendingProvider is the last line of defence: popular videos, computed
// without reference to the viewer or to any model.
type TrendingProvider interface {
	Trending(ctx context.Context, topN int) ([]string, error)
}

// ModelSource hands out the current bundle. Satisfied by *onnxmodel.Registry;
// an interface here so the service can be tested without one.
type ModelSource interface {
	Current() *onnxmodel.Bundle
}

// Options configures a Service.
type Options struct {
	Store    vectorstore.VectorStore
	Models   ModelSource
	Features FeatureStore
	Trending TrendingProvider
	Logger   *slog.Logger

	// CandidateMultiplier decides how many candidates retrieval fetches per
	// requested result. Ranking can only reorder what retrieval hands it, so
	// too small a pool caps quality no matter how good the model is; too large
	// a pool costs inference time on items that will never be shown. Ten is the
	// usual starting point.
	CandidateMultiplier int
	// MaxCandidates caps the pool regardless of topN, bounding the worst-case
	// inference batch.
	MaxCandidates int
	// RetrievalTimeout bounds the vector store call so one slow query cannot
	// hold the whole request past its budget.
	RetrievalTimeout time.Duration
	// Now is injectable so freshness features are testable.
	Now func() time.Time
}

// Defaults for Options.
const (
	DefaultCandidateMultiplier = 10
	DefaultMaxCandidates       = 500
	DefaultRetrievalTimeout    = 250 * time.Millisecond
)

type service struct {
	options Options
}

// New returns a Service.
//
// Only the vector store is genuinely required. Everything else has a documented
// degradation, and refusing to construct without them would make the fallback
// paths unreachable.
func New(options Options) (Service, error) {
	if options.Store == nil {
		return nil, errors.New("recommendation: a vector store is required")
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.CandidateMultiplier <= 0 {
		options.CandidateMultiplier = DefaultCandidateMultiplier
	}
	if options.MaxCandidates <= 0 {
		options.MaxCandidates = DefaultMaxCandidates
	}
	if options.RetrievalTimeout <= 0 {
		options.RetrievalTimeout = DefaultRetrievalTimeout
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &service{options: options}, nil
}

func (s *service) GetRecommendations(
	ctx context.Context, userID string, watchHistory []string, topN int,
) ([]VideoScore, error) {
	if topN <= 0 {
		return nil, fmt.Errorf("recommendation: topN must be positive, got %d", topN)
	}

	candidates, err := s.retrieve(ctx, watchHistory, s.candidatePoolSize(topN))
	if err != nil {
		s.options.Logger.WarnContext(ctx, "candidate generation failed, falling back",
			"user", userID, "error", err)
		return s.trending(ctx, topN)
	}
	if len(candidates) == 0 {
		s.options.Logger.WarnContext(ctx, "candidate generation returned nothing, falling back",
			"user", userID)
		return s.trending(ctx, topN)
	}

	ranked, err := s.rank(ctx, userID, candidates)
	if err != nil {
		// Retrieval already produced a relevance ordering. Serving it unranked
		// is a real degradation but a much smaller one than abandoning a good
		// candidate set because the second stage is unavailable.
		s.options.Logger.WarnContext(ctx, "ranking failed, serving retrieval order",
			"user", userID, "error", err)
		return truncate(byRetrievalScore(candidates), topN), nil
	}
	return truncate(ranked, topN), nil
}

// candidatePoolSize decides how many candidates to retrieve for a request.
func (s *service) candidatePoolSize(topN int) int {
	size := topN * s.options.CandidateMultiplier
	if size > s.options.MaxCandidates {
		return s.options.MaxCandidates
	}
	return size
}

// retrieve builds a query vector from the watch history and searches.
//
// The query is the mean of the embeddings of what the viewer watched, which is
// the serving-time counterpart of the user tower's masked-mean pooling. That
// equivalence is why the user tower never has to run per request: the same
// vector can be assembled from the video embeddings already in the index.
func (s *service) retrieve(
	ctx context.Context, watchHistory []string, poolSize int,
) ([]vectorstore.Candidate, error) {
	ctx, cancel := context.WithTimeout(ctx, s.options.RetrievalTimeout)
	defer cancel()

	if len(watchHistory) == 0 {
		// No history, no query vector, nothing for retrieval to be near. This
		// is a cold viewer, not a failure.
		return nil, nil
	}

	watched, err := s.options.Store.Lookup(ctx, watchHistory)
	if err != nil {
		return nil, fmt.Errorf("recommendation: history lookup: %w", err)
	}

	vectors := make([][]float32, 0, len(watched))
	for _, embedding := range watched {
		vectors = append(vectors, embedding.Vector)
	}
	query, err := vectorstore.MeanVector(vectors)
	if err != nil {
		return nil, fmt.Errorf("recommendation: query vector: %w", err)
	}
	if query == nil {
		// Every watched video predates the current index. Retrieval has nothing
		// to search from.
		return nil, nil
	}

	// Over-fetch by the history size so filtering out already-watched videos
	// cannot leave the pool short.
	candidates, err := s.options.Store.Search(ctx, query, poolSize+len(watchHistory))
	if err != nil {
		return nil, fmt.Errorf("recommendation: search: %w", err)
	}

	seen := make(map[string]struct{}, len(watchHistory))
	for _, id := range watchHistory {
		seen[id] = struct{}{}
	}
	filtered := candidates[:0]
	for _, candidate := range candidates {
		if _, watched := seen[candidate.VideoID]; watched {
			continue
		}
		filtered = append(filtered, candidate)
	}
	if len(filtered) > poolSize {
		filtered = filtered[:poolSize]
	}
	return filtered, nil
}

// rank scores candidates with the ranking model.
func (s *service) rank(
	ctx context.Context, userID string, candidates []vectorstore.Candidate,
) ([]VideoScore, error) {
	if s.options.Models == nil {
		return nil, onnxmodel.ErrUnavailable
	}
	bundle := s.options.Models.Current()
	if bundle == nil || bundle.Ranker == nil {
		return nil, onnxmodel.ErrUnavailable
	}

	user := UserFeatures{}
	if s.options.Features != nil {
		loaded, err := s.options.Features.UserFeatures(ctx, userID)
		if err != nil {
			// A viewer whose features cannot be read still gets ranked, on the
			// neutral prior. Refusing would mean a feature store blip takes the
			// ranking model down with it.
			s.options.Logger.WarnContext(ctx, "user features unavailable, using neutral prior",
				"user", userID, "error", err)
		} else {
			user = loaded
		}
	}

	rows := BuildFeatureRows(candidates, user, s.options.Now())
	scores, err := bundle.Ranker.Score(ctx, rows)
	if err != nil {
		return nil, err
	}
	if len(scores) != len(candidates) {
		return nil, fmt.Errorf(
			"recommendation: ranker returned %d scores for %d candidates",
			len(scores), len(candidates),
		)
	}

	ranked := make([]VideoScore, len(candidates))
	for i, candidate := range candidates {
		ranked[i] = VideoScore{
			VideoID: candidate.VideoID,
			Score:   scores[i],
			Source:  SourceRanked,
		}
	}
	sortScores(ranked)
	return ranked, nil
}

// trending is the fallback: popular videos, no model, no viewer.
func (s *service) trending(ctx context.Context, topN int) ([]VideoScore, error) {
	if s.options.Trending == nil {
		// Nothing left to try. An empty feed is a poor outcome but it is a
		// answer; an error here would turn a degraded recommender into a
		// broken page.
		s.options.Logger.ErrorContext(ctx, "no trending provider configured; serving nothing")
		return nil, nil
	}

	ids, err := s.options.Trending.Trending(ctx, topN)
	if err != nil {
		return nil, fmt.Errorf("recommendation: trending fallback: %w", err)
	}

	results := make([]VideoScore, 0, len(ids))
	for i, id := range ids {
		results = append(results, VideoScore{
			VideoID: id,
			// Descending so the order survives any later sort, without
			// pretending to be a model score.
			Score:  float32(len(ids) - i),
			Source: SourceTrending,
		})
	}
	return truncate(results, topN), nil
}

// byRetrievalScore converts candidates to results ordered by similarity.
func byRetrievalScore(candidates []vectorstore.Candidate) []VideoScore {
	results := make([]VideoScore, len(candidates))
	for i, candidate := range candidates {
		results[i] = VideoScore{
			VideoID: candidate.VideoID,
			Score:   candidate.Score,
			Source:  SourceRetrieval,
		}
	}
	sortScores(results)
	return results
}

// sortScores orders by descending score, breaking ties by id.
//
// The tie-break is not cosmetic: equal scores are common once a fallback
// assigns them, and an unstable order makes a feed reshuffle between two
// identical requests for no reason the viewer can perceive.
func sortScores(scores []VideoScore) {
	sort.Slice(scores, func(a, b int) bool {
		if scores[a].Score != scores[b].Score {
			return scores[a].Score > scores[b].Score
		}
		return scores[a].VideoID < scores[b].VideoID
	})
}

func truncate(scores []VideoScore, topN int) []VideoScore {
	if len(scores) > topN {
		return scores[:topN]
	}
	return scores
}
