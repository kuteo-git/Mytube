package vectorstore

import (
	"context"
	"sort"
	"sync"
)

// MemoryStore keeps every embedding in memory and scans all of them per query.
//
// Brute force is the right algorithm at this scale, not a placeholder for a
// real one. Approximate nearest neighbour indexes buy sublinear search by
// paying for index construction and accepting imperfect recall; both are only
// worth it once a linear scan stops being cheap. A catalogue of a few thousand
// videos at 128 dimensions is a few hundred thousand multiply-adds — tens of
// microseconds, with no network hop, no index build after each training run,
// and exact results. pgvector becomes the better answer somewhere in the
// hundreds of thousands of videos; below that it is slower, because a round
// trip to Postgres costs more than the scan it replaces.
type MemoryStore struct {
	mu         sync.RWMutex
	embeddings []Embedding
	byID       map[string]int
	dimension  int
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{byID: make(map[string]int)}
}

// Replace swaps the whole index atomically from a reader's point of view.
func (s *MemoryStore) Replace(_ context.Context, embeddings []Embedding) error {
	index := make(map[string]int, len(embeddings))
	dimension := 0
	for i, embedding := range embeddings {
		index[embedding.VideoID] = i
		if i == 0 {
			dimension = len(embedding.Vector)
			continue
		}
		if len(embedding.Vector) != dimension {
			return ErrDimensionMismatch
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.embeddings = embeddings
	s.byID = index
	s.dimension = dimension
	return nil
}

// Search scans every embedding and returns the closest topN.
func (s *MemoryStore) Search(ctx context.Context, query []float32, topN int) ([]Candidate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.embeddings) == 0 {
		return nil, ErrEmptyIndex
	}
	if len(query) != s.dimension {
		return nil, ErrDimensionMismatch
	}
	if topN <= 0 {
		return nil, nil
	}

	candidates := make([]Candidate, 0, len(s.embeddings))
	for i := range s.embeddings {
		// Checked per batch rather than per vector: a cancellation check is
		// comparable in cost to the dot product it guards.
		if i%512 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		score, err := Dot(query, s.embeddings[i].Vector)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, Candidate{Embedding: s.embeddings[i], Score: score})
	}

	sort.Slice(candidates, func(a, b int) bool {
		if candidates[a].Score != candidates[b].Score {
			return candidates[a].Score > candidates[b].Score
		}
		// Ties broken by id so results are stable across runs; an unstable
		// order makes a feed reshuffle for no reason the viewer can see.
		return candidates[a].VideoID < candidates[b].VideoID
	})

	if len(candidates) > topN {
		candidates = candidates[:topN]
	}
	return candidates, nil
}

// Lookup returns the embeddings for the given ids, silently skipping unknowns.
//
// An id with no embedding is normal: it was watched before the last training
// run indexed it. Failing the request over that would make every new video a
// source of errors.
func (s *MemoryStore) Lookup(_ context.Context, videoIDs []string) ([]Embedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	found := make([]Embedding, 0, len(videoIDs))
	for _, id := range videoIDs {
		if index, ok := s.byID[id]; ok {
			found = append(found, s.embeddings[index])
		}
	}
	return found, nil
}

// Len reports how many vectors are indexed.
func (s *MemoryStore) Len(_ context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.embeddings), nil
}
