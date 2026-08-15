// Package postgres implements domain.JobStore against the ingest schema.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const jobColumns = `id, source_url, video_id, title, state, preferred_height,
                    progress, downloaded_bytes, total_bytes, error_message,
                    attempts, requested_by, created_at, finished_at`

func scanJob(row pgx.Row) (domain.Job, error) {
	var (
		j     domain.Job
		state string
	)
	err := row.Scan(&j.ID, &j.SourceURL, &j.VideoID, &j.Title, &state, &j.PreferredHeight,
		&j.Progress, &j.DownloadedBytes, &j.TotalBytes, &j.ErrorMessage,
		&j.Attempts, &j.RequestedBy, &j.CreatedAt, &j.FinishedAt)
	j.State = domain.JobState(state)
	return j, err
}

// Enqueue is idempotent while a job for the same URL is still active, so
// double-clicking "add" attaches to the running download instead of starting a
// second one.
func (s *Store) Enqueue(ctx context.Context, job domain.Job) (domain.Job, error) {
	existing, err := scanJob(s.pool.QueryRow(ctx,
		`SELECT `+jobColumns+` FROM jobs
		 WHERE source_url = $1 AND state IN ('QUEUED', 'RUNNING')`, job.SourceURL))
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Job{}, err
	}

	return scanJob(s.pool.QueryRow(ctx, `
		INSERT INTO jobs (id, source_url, preferred_height, requested_by)
		VALUES ($1, $2, $3, $4)
		RETURNING `+jobColumns,
		job.ID, job.SourceURL, job.PreferredHeight, job.RequestedBy))
}

func (s *Store) Get(ctx context.Context, jobID string) (domain.Job, error) {
	j, err := scanJob(s.pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = $1`, jobID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Job{}, fmt.Errorf("job %s: %w", jobID, domain.ErrNotFound)
	}
	return j, err
}

func (s *Store) List(ctx context.Context, activeOnly, hideDismissed bool, limit int32) ([]domain.Job, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	var where []string
	if activeOnly {
		where = append(where, `state IN ('QUEUED', 'RUNNING')`)
	}
	if hideDismissed {
		where = append(where, `dismissed_at IS NULL`)
	}

	query := `SELECT ` + jobColumns + ` FROM jobs`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	// Unfinished work first, then by recency.
	//
	// Recency alone is not enough once the queue has any history. The list is
	// capped, and a burst of completed jobs — a scan, or several videos opened
	// in a row — pushes an older running transfer off the end of it. The player
	// watches this list to know when a copy has landed, so a download that
	// falls off simply never appears to progress or finish: the picture stays
	// on the low-resolution upstream until the page is reloaded, which is
	// exactly how it was found.
	query += ` ORDER BY (state IN ('QUEUED', 'RUNNING')) DESC, created_at DESC LIMIT $1`

	rows, err := s.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []domain.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *Store) Cancel(ctx context.Context, jobID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs SET state = 'CANCELLED', finished_at = now()
		WHERE id = $1 AND state IN ('QUEUED', 'RUNNING')`, jobID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("job %s is not cancellable: %w", jobID, domain.ErrNotFound)
	}
	return nil
}

// Dismiss hides a finished job from the Activity page.
//
// Terminal states only. Hiding a job still queued or running would take work
// off the one page that reports it while leaving it running underneath, and
// the button that does that on those rows already has a meaning — it cancels.
func (s *Store) Dismiss(ctx context.Context, jobID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs SET dismissed_at = now()
		WHERE id = $1 AND state NOT IN ('QUEUED', 'RUNNING') AND dismissed_at IS NULL`, jobID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("job %s cannot be dismissed: %w", jobID, domain.ErrNotFound)
	}
	return nil
}

// DismissByState hides every job with a given state. Returns the count.
func (s *Store) DismissByState(ctx context.Context, state string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs SET dismissed_at = now()
		WHERE state = $1 AND dismissed_at IS NULL`, state)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// CancelForVideo stops whatever transfer is running for a video, if any.
//
// By video rather than by job id because that is what the caller knows: the
// watch page is leaving a video, and has no reason to have learned which job
// happens to be carrying it.
//
// Not finding one is success. Leaving a video whose copy already landed, or
// which never needed one, is the ordinary case — an error there would make the
// common path noisy.
func (s *Store) CancelForVideo(ctx context.Context, videoID string) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs SET state = 'CANCELLED', finished_at = now()
		WHERE video_id = $1 AND state IN ('QUEUED', 'RUNNING')`, videoID)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// Claim takes the oldest queued job. SKIP LOCKED lets several workers run
// without ever handing the same job to two of them, and the lease means a
// worker that dies mid-download does not strand its job forever.
func (s *Store) Claim(ctx context.Context, lease time.Duration) (domain.Job, error) {
	j, err := scanJob(s.pool.QueryRow(ctx, `
		UPDATE jobs SET
			state = 'RUNNING',
			attempts = attempts + 1,
			started_at = now(),
			lease_expires_at = now() + $1::interval
		WHERE id = (
			SELECT id FROM jobs
			WHERE state = 'QUEUED'
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING `+jobColumns, lease.String()))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Job{}, domain.ErrNotFound
	}
	return j, err
}

func (s *Store) Heartbeat(ctx context.Context, jobID string, lease time.Duration, p domain.Progress) error {
	// Only a job still running may be heartbeated, and a heartbeat that lands on
	// nothing is how the transfer learns it was cancelled.
	//
	// The cancel writes to this row from another process — the worker runs on
	// its own — so this is the one channel between them. Without it, cancelling
	// marked the row and left yt-dlp running: for an ordinary video that wastes
	// a download nobody wants, and for a livestream it never ends at all, which
	// held the single worker slot for as long as the process lived and left
	// every later job queued at 0%.
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs SET
			progress = $2,
			downloaded_bytes = $3,
			total_bytes = $4,
			lease_expires_at = now() + $5::interval
		WHERE id = $1 AND state = 'RUNNING'`,
		jobID, p.Fraction, p.DownloadedBytes, p.TotalBytes, lease.String())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrJobNotRunning
	}
	return nil
}

func (s *Store) MarkResolved(ctx context.Context, jobID, videoID, title string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE jobs SET video_id = $2, title = $3 WHERE id = $1`, jobID, videoID, title)
	return err
}

func (s *Store) Finish(ctx context.Context, jobID string, state domain.JobState, errorMessage string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE jobs SET state = $2, error_message = $3, finished_at = now(),
		                lease_expires_at = NULL,
		                progress = CASE WHEN $2 = 'SUCCEEDED' THEN 1 ELSE progress END
		WHERE id = $1`, jobID, string(state), errorMessage)
	return err
}

// ReleaseExpired requeues jobs whose worker stopped heartbeating, giving up
// after a few attempts so a permanently broken URL cannot loop forever.
func (s *Store) ReleaseExpired(ctx context.Context) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs SET
			state = CASE WHEN attempts >= 3 THEN 'FAILED' ELSE 'QUEUED' END,
			error_message = CASE WHEN attempts >= 3
			                     THEN 'worker stopped responding' ELSE error_message END,
			finished_at = CASE WHEN attempts >= 3 THEN now() ELSE NULL END,
			lease_expires_at = NULL
		WHERE state = 'RUNNING' AND lease_expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// NextOffset reports how far into a source the library has already been
// filled. A source never seen before starts at zero.
func (s *Store) NextOffset(ctx context.Context, sourceURL string) (int32, error) {
	var offset int32
	err := s.pool.QueryRow(ctx,
		`SELECT next_offset FROM source_cursors WHERE source_url = $1`, sourceURL).Scan(&offset)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return offset, err
}

// AdvanceOffset moves the cursor forward by the number of entries just read,
// so the next deepening pass resumes past them instead of re-reading them.
func (s *Store) AdvanceOffset(ctx context.Context, sourceURL string, by int32) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO source_cursors (source_url, next_offset)
		VALUES ($1, $2)
		ON CONFLICT (source_url) DO UPDATE
		SET next_offset = source_cursors.next_offset + EXCLUDED.next_offset,
		    updated_at = now()`, sourceURL, by)
	return err
}

// Refusals upstream has made permanently.
//
// Kept in ingest's own schema because "can this URL be fetched" is ingest's
// question about upstream, not the catalogue's about the library.

func (s *Store) MarkUnavailable(ctx context.Context, u domain.UnavailableSource) error {
	// The first refusal wins. A later one says nothing new, and rewriting
	// first_seen_at would lose the only thing this table records about time.
	_, err := s.pool.Exec(ctx, `
		INSERT INTO unavailable_sources (source_url, video_id, reason, detail)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (source_url) DO UPDATE
		SET video_id = CASE WHEN unavailable_sources.video_id = ''
		                    THEN EXCLUDED.video_id
		                    ELSE unavailable_sources.video_id END`,
		u.SourceURL, u.VideoID, string(u.Reason), u.Detail)
	return err
}

func (s *Store) UnavailableSourceFor(ctx context.Context, sourceURL string) (domain.UnavailableSource, bool, error) {
	var (
		u      domain.UnavailableSource
		reason string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT source_url, video_id, reason, detail, first_seen_at
		FROM unavailable_sources WHERE source_url = $1`, sourceURL).
		Scan(&u.SourceURL, &u.VideoID, &reason, &u.Detail, &u.FirstSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.UnavailableSource{}, false, nil
	}
	if err != nil {
		return domain.UnavailableSource{}, false, err
	}
	u.Reason = domain.UnavailableReason(reason)
	return u, true, nil
}

func (s *Store) ClearUnavailable(ctx context.Context, sourceURL string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM unavailable_sources WHERE source_url = $1`, sourceURL)
	return err
}

func (s *Store) UnreportedUnavailable(ctx context.Context, limit int32) ([]domain.UnavailableSource, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT source_url, video_id, reason, detail, first_seen_at
		FROM unavailable_sources
		WHERE reported_at IS NULL
		ORDER BY first_seen_at
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.UnavailableSource
	for rows.Next() {
		var (
			u      domain.UnavailableSource
			reason string
		)
		if err := rows.Scan(&u.SourceURL, &u.VideoID, &reason, &u.Detail, &u.FirstSeenAt); err != nil {
			return nil, err
		}
		u.Reason = domain.UnavailableReason(reason)
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) MarkUnavailableReported(ctx context.Context, sourceURL string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE unavailable_sources SET reported_at = now() WHERE source_url = $1`, sourceURL)
	return err
}
