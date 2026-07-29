// Package vectorstore holds the candidate generation index: video embeddings
// produced by the training pipeline, searchable by similarity.
//
// The interface exists so the backing store is a deployment decision rather
// than a structural one. Two implementations ship: an in-memory brute-force
// scan and pgvector. A Qdrant implementation would satisfy the same interface
// and require no change above it.
package vectorstore

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// ErrEmptyIndex reports a search against a store that holds no vectors. It is
// separated from a transport failure on purpose: an empty index means the
// training pipeline has not published embeddings yet, which is a deployment
// state the caller can fall back from, not an outage to retry.
var ErrEmptyIndex = errors.New("vectorstore: index is empty")

// ErrDimensionMismatch reports a query whose width disagrees with the index.
// Almost always this means the service is searching with vectors from one
// training run against an index written by another.
var ErrDimensionMismatch = errors.New("vectorstore: query dimension does not match index")

// Embedding is one video's position in the shared space, along with the static
// attributes ranking needs. Carrying them here rather than fetching them again
// per candidate keeps a whole round trip out of the request path.
type Embedding struct {
	VideoID string
	Vector  []float32
	// CompletionRateAvg is the video's average watch ratio across all viewers,
	// precomputed by the training pipeline.
	CompletionRateAvg float32
	// UploadedAtUnix is the upload time, used for the freshness feature. Zero
	// means unknown.
	UploadedAtUnix int64
	CreatorID      string
	CategoryID     int32
}

// Candidate is a search hit: a video and how close it sat to the query.
type Candidate struct {
	Embedding
	// Score is cosine similarity in [-1, 1]. Both towers emit unit vectors, so
	// the dot product is already the cosine and needs no normalisation here.
	Score float32
}

// VectorStore is the candidate generation index.
//
// Implementations must be safe for concurrent use: one instance serves every
// request, and the recommendation service does not serialise access to it.
type VectorStore interface {
	// Search returns at most topN candidates ordered by descending similarity.
	// Returning fewer than requested is not an error — a young catalogue simply
	// has less to offer — but returning none is reported as ErrEmptyIndex so
	// the caller can tell "nothing indexed" from "nothing similar".
	Search(ctx context.Context, query []float32, topN int) ([]Candidate, error)

	// Lookup returns the stored embeddings for specific videos, skipping ids it
	// does not hold. Used to assemble a query vector from a watch history.
	Lookup(ctx context.Context, videoIDs []string) ([]Embedding, error)

	// Replace swaps the entire index. Publishing a training run is a wholesale
	// replacement rather than an upsert: a partially updated index mixes
	// vectors from two runs, and distances between them are meaningless.
	Replace(ctx context.Context, embeddings []Embedding) error

	// Len reports how many vectors are indexed, for health reporting.
	Len(ctx context.Context) (int, error)
}

// Dot returns the dot product of two equal-length vectors.
//
// For unit vectors — which is what both towers emit — this is cosine
// similarity.
func Dot(a, b []float32) (float32, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("%w: %d vs %d", ErrDimensionMismatch, len(a), len(b))
	}
	var sum float32
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum, nil
}

// MeanVector averages a set of vectors and normalises the result to unit
// length.
//
// This is how a watch history becomes a query: the mean of what someone
// watched, pointed back into the same space the videos live in. It is the
// serving-time counterpart of the user tower's masked-mean pooling, which is
// why the user tower does not need to run per request at all.
//
// Returns nil when there is nothing to average, which the caller must treat as
// "no history" rather than as a zero vector — a zero vector is a real position
// in the space and would retrieve whatever happens to sit near the origin.
func MeanVector(vectors [][]float32) ([]float32, error) {
	if len(vectors) == 0 {
		return nil, nil
	}

	width := len(vectors[0])
	sum := make([]float32, width)
	for _, vector := range vectors {
		if len(vector) != width {
			return nil, fmt.Errorf("%w: %d vs %d", ErrDimensionMismatch, len(vector), width)
		}
		for i, value := range vector {
			sum[i] += value
		}
	}

	var magnitude float32
	for _, value := range sum {
		magnitude += value * value
	}
	if magnitude == 0 {
		// Vectors that cancel out exactly. Rare, but a zero query would return
		// arbitrary neighbours, so say so instead.
		return nil, nil
	}

	inverse := float32(1.0 / math.Sqrt(float64(magnitude)))
	for i := range sum {
		sum[i] *= inverse
	}
	return sum, nil
}
