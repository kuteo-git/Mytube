package postgres

import (
	"context"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// RecordScan saves one pass and prunes the ones that have stopped being useful.
//
// Pruned by age rather than by count, because the question this table answers
// is asked in days — "has it run this week" — and a count would quietly change
// meaning with SCAN_INTERVAL: five hundred rows is three weeks at hourly and
// ten days at half-hourly, without anybody touching the setting.
//
// Pruning here rather than on a timer ties the table's shrinking to the only
// thing that makes it grow, and means there is no second schedule to reason
// about.
func (s *Store) RecordScan(ctx context.Context, r domain.ScanResult, retain time.Duration) error {
	errs := r.Errors
	if errs == nil {
		// pgx encodes a nil slice as NULL, which the NOT NULL column rejects.
		// A pass with no errors is the ordinary case, so this is the path that
		// would break first (CLAUDE.md §8b).
		errs = []string{}
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO scans (started_at, duration_ms, sources_scanned, sources_failed,
		                   videos_seen, videos_added, errors)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		r.StartedAt, r.Duration.Milliseconds(), r.SourcesScanned, r.SourcesFailed,
		r.VideosSeen, r.VideosAdded, errs); err != nil {
		return err
	}

	if retain <= 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx,
		`DELETE FROM scans WHERE started_at < now() - $1::interval`, retain.String())
	return err
}

func (s *Store) ListScans(ctx context.Context, limit, offset int32) ([]domain.ScanResult, int32, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	var total int32
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM scans`).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT started_at, duration_ms, sources_scanned, sources_failed,
		       videos_seen, videos_added, errors
		FROM scans
		ORDER BY started_at DESC
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []domain.ScanResult
	for rows.Next() {
		var (
			r          domain.ScanResult
			durationMs int64
		)
		if err := rows.Scan(&r.StartedAt, &durationMs, &r.SourcesScanned, &r.SourcesFailed,
			&r.VideosSeen, &r.VideosAdded, &r.Errors); err != nil {
			return nil, 0, err
		}
		r.Duration = time.Duration(durationMs) * time.Millisecond
		out = append(out, r)
	}
	return out, total, rows.Err()
}
