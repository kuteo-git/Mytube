// Package postgres implements domain.SignalStore against the recsys schema.
package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) AppendSignal(ctx context.Context, sig domain.Signal) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO signals (user_id, type, video_id, query, watched_fraction, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		sig.UserID, string(sig.Type), sig.VideoID, sig.Query, sig.WatchedFraction, sig.OccurredAt)
	return err
}

func (s *Store) RecordImpressions(ctx context.Context, userID string, videoIDs []string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO impressions (user_id, video_id, shown_at)
		SELECT $1, unnest($2::text[]), now()
		ON CONFLICT (user_id, video_id) DO UPDATE SET shown_at = now()`,
		userID, videoIDs)
	return err
}

// BuildProfile derives everything ranking needs in three queries. It is
// recomputed per request rather than cached, which is what lets the feed react
// to the video that just finished playing.
func (s *Store) BuildProfile(ctx context.Context, userID string, impressionWindow time.Duration) (domain.UserProfile, error) {
	profile := domain.UserProfile{
		WatchedFraction:   map[string]float32{},
		Liked:             map[string]bool{},
		Disliked:          map[string]bool{},
		Subscribed:        map[string]bool{},
		RecentImpressions: map[string]bool{},
		RecentlyWatched:   map[string]bool{},
	}
	if userID == "" {
		return profile, nil
	}

	// Highest watched fraction per video wins: a partial rewatch must not
	// downgrade a video the user already finished.
	rows, err := s.pool.Query(ctx, `
		SELECT video_id, max(watched_fraction)
		FROM signals
		WHERE user_id = $1 AND type = 'WATCH' AND video_id <> ''
		GROUP BY video_id`, userID)
	if err != nil {
		return profile, err
	}
	for rows.Next() {
		var (
			videoID  string
			fraction float32
		)
		if err := rows.Scan(&videoID, &fraction); err != nil {
			rows.Close()
			return profile, err
		}
		profile.WatchedFraction[videoID] = fraction
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return profile, err
	}

	// Latest reaction and subscription state per target, so an undo actually
	// takes effect instead of being outvoted by its own history.
	rows, err = s.pool.Query(ctx, `
		SELECT DISTINCT ON (video_id, type_group) video_id, type_group, type
		FROM (
			SELECT video_id, occurred_at, type,
			       CASE WHEN type IN ('LIKE', 'DISLIKE') THEN 'reaction'
			            ELSE 'subscription' END AS type_group
			FROM signals
			WHERE user_id = $1
			  AND type IN ('LIKE', 'DISLIKE', 'SUBSCRIBE', 'UNSUBSCRIBE')
			  AND video_id <> ''
		) t
		ORDER BY video_id, type_group, occurred_at DESC`, userID)
	if err != nil {
		return profile, err
	}
	for rows.Next() {
		var target, group, signalType string
		if err := rows.Scan(&target, &group, &signalType); err != nil {
			rows.Close()
			return profile, err
		}
		switch signalType {
		case string(domain.SignalLike):
			profile.Liked[target] = true
		case string(domain.SignalDislike):
			profile.Disliked[target] = true
		case string(domain.SignalSubscribe):
			// SUBSCRIBE signals carry the channel id in video_id.
			profile.Subscribed[target] = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return profile, err
	}

	rows, err = s.pool.Query(ctx, `
		SELECT video_id FROM impressions
		WHERE user_id = $1 AND shown_at > now() - $2::interval`,
		userID, impressionWindow.String())
	if err != nil {
		return profile, err
	}
	for rows.Next() {
		var videoID string
		if err := rows.Scan(&videoID); err != nil {
			rows.Close()
			return profile, err
		}
		profile.RecentImpressions[videoID] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return profile, err
	}

	// Videos this viewer has watched in the last little while.
	//
	// Separate from WatchedFraction, which says whether something was ever
	// watched and says nothing about when. What follows a video has to know
	// about "just now" specifically: two videos on the same channel with the
	// same topic each rank first in the other's suggestions, so without this the
	// pair is a trap — press next twice and you are back where you started.
	rows, err = s.pool.Query(ctx, `
		SELECT DISTINCT video_id FROM signals
		WHERE user_id = $1 AND type = 'WATCH' AND video_id <> ''
		  AND occurred_at > now() - $2::interval`,
		userID, recentWatchWindow.String())
	if err != nil {
		return profile, err
	}
	for rows.Next() {
		var videoID string
		if err := rows.Scan(&videoID); err != nil {
			rows.Close()
			return profile, err
		}
		profile.RecentlyWatched[videoID] = true
	}
	rows.Close()

	return profile, rows.Err()
}

// How recently a video must have been watched to count as "just now".
//
// Long enough to cover a sitting, short enough that a video watched this
// morning is a fair suggestion again this evening.
const recentWatchWindow = 3 * time.Hour

// MostWatched ranks by accumulated watch signals.
//
// Signals are appended every few seconds of playback rather than once per
// open, so the count is proportional to time spent. A video watched twice all
// the way through therefore outranks one opened ten times and abandoned, which
// is what "played the most" should mean.
func (s *Store) MostWatched(ctx context.Context, userID string, limit int32) ([]domain.RankedVideo, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows, err := s.pool.Query(ctx, `
		SELECT video_id, count(*)::float8 AS weight
		FROM signals
		WHERE user_id = $1 AND type = 'WATCH' AND video_id <> ''
		GROUP BY video_id
		ORDER BY weight DESC, video_id
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.RankedVideo
	for rows.Next() {
		var v domain.RankedVideo
		if err := rows.Scan(&v.VideoID, &v.Score); err != nil {
			return nil, err
		}
		v.Reason = domain.ReasonRewatch
		out = append(out, v)
	}
	return out, rows.Err()
}

// VideoRetention reports how far the average viewer gets through each video.
//
// Computed here rather than asked of catalog, because the watch signals are
// recsys's own and no service reads another's tables. Catalog does not know
// how long anything was watched for, and that boundary is what keeps this a
// set of services rather than one program in four processes.
//
// The inner max matters more than it looks. Watch signals are appended on a
// timer while a video plays, so one viewing leaves a trail of rows climbing
// from near zero to wherever the viewer stopped. Averaging those rows directly
// would measure "the average moment at which we happened to take a reading",
// which is roughly half of every video no matter how good it is. Taking each
// viewer's furthest point first, then averaging across viewers, measures the
// thing the name claims.
func (s *Store) VideoRetention(ctx context.Context) (map[string]float32, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT video_id, avg(furthest)::float4
		FROM (
			SELECT video_id, user_id, max(watched_fraction) AS furthest
			FROM signals
			WHERE type = 'WATCH' AND video_id <> ''
			GROUP BY video_id, user_id
		) per_viewer
		GROUP BY video_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	retention := map[string]float32{}
	for rows.Next() {
		var (
			videoID string
			average float32
		)
		if err := rows.Scan(&videoID, &average); err != nil {
			return nil, err
		}
		retention[videoID] = average
	}
	return retention, rows.Err()
}
