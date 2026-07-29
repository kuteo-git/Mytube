package vectorstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgVectorStore keeps embeddings in Postgres with the `vector` extension.
//
// The right choice once the catalogue outgrows a linear scan: the index lives
// beside the data, survives a restart, and is shared by every replica instead
// of being rebuilt in each process. Below that scale it is slower than
// MemoryStore, because a round trip costs more than the scan it replaces.
//
// Requires the extension, which is not installed by default:
//
//	brew install pgvector
//	psql -c 'CREATE EXTENSION IF NOT EXISTS vector'
type PgVectorStore struct {
	pool      *pgxpool.Pool
	table     string
	dimension int
}

// NewPgVectorStore returns a store backed by the given pool.
//
// The dimension is fixed at construction because the column type is
// `vector(n)`: it cannot be inferred and a mismatch is rejected by Postgres
// rather than silently truncated.
func NewPgVectorStore(pool *pgxpool.Pool, table string, dimension int) *PgVectorStore {
	return &PgVectorStore{pool: pool, table: table, dimension: dimension}
}

// Migrate creates the table and index if they do not exist.
//
// The index is IVFFlat rather than HNSW: it builds far faster, which matters
// when every training run republishes the whole catalogue, and its recall is
// indistinguishable at this size. `lists` is deliberately a function of row
// count — a fixed value is either wasteful on a small table or useless on a
// large one.
func (s *PgVectorStore) Migrate(ctx context.Context, lists int) error {
	statements := []string{
		"CREATE EXTENSION IF NOT EXISTS vector",
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			video_id            text PRIMARY KEY,
			embedding           vector(%d) NOT NULL,
			completion_rate_avg real   NOT NULL DEFAULT 0,
			uploaded_at_unix    bigint NOT NULL DEFAULT 0,
			creator_id          text   NOT NULL DEFAULT '',
			category_id         int    NOT NULL DEFAULT 0
		)`, s.table, s.dimension),
		fmt.Sprintf(
			`CREATE INDEX IF NOT EXISTS %s_embedding_idx ON %s
			 USING ivfflat (embedding vector_cosine_ops) WITH (lists = %d)`,
			strings.ReplaceAll(s.table, ".", "_"), s.table, lists,
		),
	}
	for _, statement := range statements {
		if _, err := s.pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("vectorstore: migrate: %w", err)
		}
	}
	return nil
}

// Replace swaps the whole index inside one transaction.
//
// Truncate-and-insert rather than upsert: a half-replaced table holds vectors
// from two training runs at once, and distances between two runs' embeddings
// are meaningless — the spaces are unrelated. Doing it in a transaction means
// readers see one run or the other, never a blend.
func (s *PgVectorStore) Replace(ctx context.Context, embeddings []Embedding) error {
	transaction, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("vectorstore: begin: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	if _, err := transaction.Exec(ctx, "TRUNCATE "+s.table); err != nil {
		return fmt.Errorf("vectorstore: truncate: %w", err)
	}

	rows := make([][]any, 0, len(embeddings))
	for _, embedding := range embeddings {
		if len(embedding.Vector) != s.dimension {
			return fmt.Errorf(
				"%w: %s has %d dims, table expects %d",
				ErrDimensionMismatch, embedding.VideoID, len(embedding.Vector), s.dimension,
			)
		}
		rows = append(rows, []any{
			embedding.VideoID,
			formatVector(embedding.Vector),
			embedding.CompletionRateAvg,
			embedding.UploadedAtUnix,
			embedding.CreatorID,
			embedding.CategoryID,
		})
	}

	_, err = transaction.CopyFrom(
		ctx,
		pgx.Identifier(strings.Split(s.table, ".")),
		[]string{
			"video_id", "embedding", "completion_rate_avg",
			"uploaded_at_unix", "creator_id", "category_id",
		},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("vectorstore: copy: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("vectorstore: commit: %w", err)
	}
	return nil
}

// Search returns the nearest topN by cosine distance.
func (s *PgVectorStore) Search(ctx context.Context, query []float32, topN int) ([]Candidate, error) {
	if len(query) != s.dimension {
		return nil, fmt.Errorf("%w: %d vs %d", ErrDimensionMismatch, len(query), s.dimension)
	}

	// `<=>` is pgvector's cosine distance: 0 identical, 2 opposite. Converted
	// back to similarity below so callers see one convention regardless of
	// which store answered.
	const statement = `
		SELECT video_id, completion_rate_avg, uploaded_at_unix, creator_id, category_id,
		       1 - (embedding <=> $1) AS similarity
		FROM %s
		ORDER BY embedding <=> $1
		LIMIT $2`

	rows, err := s.pool.Query(ctx, fmt.Sprintf(statement, s.table), formatVector(query), topN)
	if err != nil {
		return nil, fmt.Errorf("vectorstore: search: %w", err)
	}
	defer rows.Close()

	candidates := make([]Candidate, 0, topN)
	for rows.Next() {
		var candidate Candidate
		if err := rows.Scan(
			&candidate.VideoID,
			&candidate.CompletionRateAvg,
			&candidate.UploadedAtUnix,
			&candidate.CreatorID,
			&candidate.CategoryID,
			&candidate.Score,
		); err != nil {
			return nil, fmt.Errorf("vectorstore: scan: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vectorstore: rows: %w", err)
	}
	if len(candidates) == 0 {
		return nil, ErrEmptyIndex
	}
	return candidates, nil
}

// Lookup returns embeddings for specific ids, skipping unknowns.
func (s *PgVectorStore) Lookup(ctx context.Context, videoIDs []string) ([]Embedding, error) {
	if len(videoIDs) == 0 {
		return nil, nil
	}

	const statement = `
		SELECT video_id, embedding, completion_rate_avg, uploaded_at_unix, creator_id, category_id
		FROM %s WHERE video_id = ANY($1)`

	rows, err := s.pool.Query(ctx, fmt.Sprintf(statement, s.table), videoIDs)
	if err != nil {
		return nil, fmt.Errorf("vectorstore: lookup: %w", err)
	}
	defer rows.Close()

	found := make([]Embedding, 0, len(videoIDs))
	for rows.Next() {
		var (
			embedding Embedding
			raw       string
		)
		if err := rows.Scan(
			&embedding.VideoID,
			&raw,
			&embedding.CompletionRateAvg,
			&embedding.UploadedAtUnix,
			&embedding.CreatorID,
			&embedding.CategoryID,
		); err != nil {
			return nil, fmt.Errorf("vectorstore: scan: %w", err)
		}
		vector, err := parseVector(raw, s.dimension)
		if err != nil {
			return nil, err
		}
		embedding.Vector = vector
		found = append(found, embedding)
	}
	return found, rows.Err()
}

// Len reports how many vectors are indexed.
func (s *PgVectorStore) Len(ctx context.Context) (int, error) {
	var count int
	row := s.pool.QueryRow(ctx, "SELECT count(*) FROM "+s.table)
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("vectorstore: len: %w", err)
	}
	return count, nil
}

// formatVector renders a vector in pgvector's text input format, `[a,b,c]`.
func formatVector(vector []float32) string {
	var builder strings.Builder
	builder.Grow(len(vector) * 12)
	builder.WriteByte('[')
	for i, value := range vector {
		if i > 0 {
			builder.WriteByte(',')
		}
		fmt.Fprintf(&builder, "%g", value)
	}
	builder.WriteByte(']')
	return builder.String()
}

// parseVector reads pgvector's text output format back into a slice.
func parseVector(raw string, dimension int) ([]float32, error) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(raw), "["), "]")
	if trimmed == "" {
		return nil, fmt.Errorf("vectorstore: empty vector literal")
	}
	parts := strings.Split(trimmed, ",")
	if len(parts) != dimension {
		return nil, fmt.Errorf("%w: %d vs %d", ErrDimensionMismatch, len(parts), dimension)
	}
	vector := make([]float32, len(parts))
	for i, part := range parts {
		var value float32
		if _, err := fmt.Sscanf(strings.TrimSpace(part), "%g", &value); err != nil {
			return nil, fmt.Errorf("vectorstore: parse vector element %d: %w", i, err)
		}
		vector[i] = value
	}
	return vector, nil
}
