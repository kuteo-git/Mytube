// Package postgres keeps cast sessions in the cast schema.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
)

// DB is what both a pool and a transaction offer, so the integration test can
// run inside a transaction it rolls back.
type DB interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Repo struct{ db DB }

func New(db DB) *Repo { return &Repo{db: db} }

// item is the queue's shape on disk, apart from the domain's so that renaming
// a field there cannot silently change what is stored.
type item struct {
	VideoID      string `json:"video_id"`
	Title        string `json:"title"`
	Channel      string `json:"channel"`
	ThumbnailURL string `json:"thumbnail_url"`
	DurationS    int    `json:"duration_s"`
}

const columns = `id, device_id, device_name, device_kind, started_by, queue, current_index, position_s,
	duration_s, volume, state, ended_reason, loaded_media_key, prev_media_key, load_acked,
	loading_since, last_seen_ok, last_progress_at, version, created_at, updated_at, narration, loaded_narrated, narration_forced_for`

func (r *Repo) Active(ctx context.Context) ([]domain.Session, error) {
	rows, err := r.db.Query(ctx, `SELECT `+columns+` FROM sessions WHERE state <> 'ended' ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list active sessions: %w", err)
	}
	defer rows.Close()
	var out []domain.Session
	for rows.Next() {
		s, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repo) Get(ctx context.Context, id string) (domain.Session, error) {
	s, err := scan(r.db.QueryRow(ctx, `SELECT `+columns+` FROM sessions WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Session{}, domain.ErrNotFound
	}
	return s, err
}

// Create ends whatever is still playing on the speaker and inserts s, in one
// transaction, so the unique index never sees two.
func (r *Repo) Create(ctx context.Context, s domain.Session, now time.Time) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`UPDATE sessions SET state = 'ended', ended_reason = $2, updated_at = $3
		 WHERE device_id = $1 AND state <> 'ended'`,
		s.DeviceID, string(domain.Replaced), now); err != nil {
		return fmt.Errorf("replace session on %q: %w", s.DeviceID, err)
	}
	args, err := values(s)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO sessions (`+columns+`) VALUES
		($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)`, args...); err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return tx.Commit(ctx)
}

// Save writes s over a row that has not ended. A row ended elsewhere is left
// as it is and reported as ErrEnded.
func (r *Repo) Save(ctx context.Context, s domain.Session) error {
	args, err := values(s)
	if err != nil {
		return err
	}
	tag, err := r.db.Exec(ctx, `UPDATE sessions SET
		device_id = $2, device_name = $3, device_kind = $4, started_by = $5, queue = $6,
		current_index = $7, position_s = $8, duration_s = $9, volume = $10, state = $11,
		ended_reason = $12, loaded_media_key = $13, prev_media_key = $14, load_acked = $15,
		loading_since = $16, last_seen_ok = $17, last_progress_at = $18, version = $19,
		created_at = $20, updated_at = $21, narration = $22, loaded_narrated = $23, narration_forced_for = $24
		WHERE id = $1 AND state <> 'ended'`, args...)
	if err != nil {
		return fmt.Errorf("save session %s: %w", s.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrEnded
	}
	return nil
}

func values(s domain.Session) ([]any, error) {
	queue := make([]item, len(s.Queue))
	for i, it := range s.Queue {
		queue[i] = item(it)
	}
	raw, err := json.Marshal(queue)
	if err != nil {
		return nil, err
	}
	var reason *string
	if s.EndedReason != "" {
		r := string(s.EndedReason)
		reason = &r
	}
	return []any{
		s.ID, s.DeviceID, s.DeviceName, string(s.DeviceKind), s.StartedBy, raw, s.CurrentIndex, s.PositionS,
		s.DurationS, s.Volume, string(s.State), reason, s.LoadedMediaKey, s.PrevMediaKey, s.LoadAcked,
		nullTime(s.LoadingSince), nullTime(s.LastSeenOK), nullTime(s.LastProgressAt), s.Version,
		s.CreatedAt, s.UpdatedAt, s.Narration, s.LoadedNarrated, s.NarrationForcedFor,
	}, nil
}

func scan(row pgx.Row) (domain.Session, error) {
	var (
		s                                    domain.Session
		kind, state                          string
		reason                               *string
		raw                                  []byte
		loadingSince, lastSeen, lastProgress *time.Time
	)
	if err := row.Scan(&s.ID, &s.DeviceID, &s.DeviceName, &kind, &s.StartedBy, &raw, &s.CurrentIndex, &s.PositionS,
		&s.DurationS, &s.Volume, &state, &reason, &s.LoadedMediaKey, &s.PrevMediaKey, &s.LoadAcked,
		&loadingSince, &lastSeen, &lastProgress, &s.Version, &s.CreatedAt, &s.UpdatedAt,
		&s.Narration, &s.LoadedNarrated, &s.NarrationForcedFor); err != nil {
		return domain.Session{}, err
	}
	var queue []item
	if err := json.Unmarshal(raw, &queue); err != nil {
		return domain.Session{}, fmt.Errorf("session %s queue: %w", s.ID, err)
	}
	s.Queue = make([]domain.Item, len(queue))
	for i, it := range queue {
		s.Queue[i] = domain.Item(it)
	}
	s.DeviceKind, s.State = domain.Kind(kind), domain.State(state)
	if reason != nil {
		s.EndedReason = domain.EndedReason(*reason)
	}
	s.LoadingSince, s.LastSeenOK, s.LastProgressAt = deref(loadingSince), deref(lastSeen), deref(lastProgress)
	return s, nil
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func deref(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
