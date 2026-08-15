// Package postgres implements domain.Repository on top of the catalog schema.
// It is the only place in the service that knows SQL exists.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucnguyen/local-youtube/services/catalog/internal/domain"
)

type Repository struct {
	pool      *pgxpool.Pool
	mediaRoot string
}

func New(pool *pgxpool.Pool, mediaRoot string) *Repository {
	return &Repository{pool: pool, mediaRoot: mediaRoot}
}

// Every video read goes through this projection so per-user state is always
// resolved in a single round trip instead of an N+1 fan-out.
const videoSelect = `
SELECT v.id, v.title, v.duration_seconds, v.view_count, v.published_at, v.added_at,
       v.thumbnail_path, v.description, v.hashtags, v.topics, v.media_state,
       v.media_path, v.size_bytes, v.pinned, v.source_url,
       c.id, c.name, c.handle, c.avatar_path, c.subscriber_count, c.verified,
       (s.user_id IS NOT NULL) AS subscribed,
       wp.position_seconds, wp.watched_fraction, wp.last_watched_at,
       r.reaction, (wl.user_id IS NOT NULL) AS in_watch_later,
       (SELECT count(*) FROM reactions lr
         WHERE lr.video_id = v.id AND lr.reaction = 'LIKE') AS like_count
FROM videos v
JOIN channels c ON c.id = v.channel_id
LEFT JOIN subscriptions  s  ON s.channel_id = c.id  AND s.user_id  = $1
LEFT JOIN watch_progress wp ON wp.video_id  = v.id  AND wp.user_id = $1
LEFT JOIN reactions      r  ON r.video_id   = v.id  AND r.user_id  = $1
LEFT JOIN watch_later    wl ON wl.video_id  = v.id  AND wl.user_id = $1
`

func scanVideo(row pgx.Row) (domain.Video, error) {
	var (
		v          domain.Video
		state      string
		subscribed bool

		// Both are unknown for videos discovered by a flat listing scan.
		publishedAt *time.Time
		viewCount   *int64

		positionSeconds *int32
		watchedFraction *float32
		lastWatchedAt   *time.Time
		reaction        *string
		inWatchLater    bool
	)

	err := row.Scan(
		&v.ID, &v.Title, &v.DurationSeconds, &viewCount, &publishedAt, &v.AddedAt,
		&v.ThumbnailPath, &v.Description, &v.Hashtags, &v.Topics, &state,
		&v.MediaPath, &v.SizeBytes, &v.Pinned, &v.SourceURL,
		&v.Channel.ID, &v.Channel.Name, &v.Channel.Handle, &v.Channel.AvatarPath,
		&v.Channel.SubscriberCount, &v.Channel.Verified,
		&subscribed,
		&positionSeconds, &watchedFraction, &lastWatchedAt,
		&reaction, &inWatchLater, &v.LikeCount,
	)
	if err != nil {
		return domain.Video{}, err
	}

	v.MediaState = domain.MediaState(state)
	v.Channel.Subscribed = subscribed
	if publishedAt != nil {
		v.PublishedAt = *publishedAt
	}
	if viewCount != nil {
		v.ViewCount = *viewCount
	}

	// User state is present only when the user has actually interacted with the
	// video; an untouched video carries no state rather than a zeroed one.
	if positionSeconds != nil || reaction != nil || inWatchLater {
		us := &domain.VideoUserState{InWatchLater: inWatchLater}
		if positionSeconds != nil {
			us.WatchPositionSeconds = *positionSeconds
		}
		if watchedFraction != nil {
			us.WatchProgress = *watchedFraction
		}
		if lastWatchedAt != nil {
			us.LastWatchedAt = *lastWatchedAt
		}
		if reaction != nil {
			us.Reaction = domain.Reaction(*reaction)
		}
		v.UserState = us
	}

	return v, nil
}

func (r *Repository) queryVideos(ctx context.Context, sql string, args ...any) ([]domain.Video, error) {
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	videos := make([]domain.Video, 0, 16)
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		videos = append(videos, v)
	}
	return videos, rows.Err()
}

func (r *Repository) GetVideo(ctx context.Context, videoID, userID string) (domain.Video, error) {
	v, err := scanVideo(r.pool.QueryRow(ctx, videoSelect+` WHERE v.id = $2`, userID, videoID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Video{}, fmt.Errorf("video %s: %w", videoID, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Video{}, err
	}

	one := []domain.Video{v}
	if err := r.loadSubtitles(ctx, one); err != nil {
		return domain.Video{}, err
	}
	return one[0], nil
}

// BatchGetVideos preserves the caller's id order, which is what keeps a ranking
// computed by the recommendation service intact after hydration.
func (r *Repository) BatchGetVideos(ctx context.Context, videoIDs []string, userID string) ([]domain.Video, error) {
	videos, err := r.queryVideos(ctx, videoSelect+` WHERE v.id = ANY($2)`, userID, videoIDs)
	if err != nil {
		return nil, err
	}

	if err := r.loadSubtitles(ctx, videos); err != nil {
		return nil, err
	}

	byID := make(map[string]domain.Video, len(videos))
	for _, v := range videos {
		byID[v.ID] = v
	}

	ordered := make([]domain.Video, 0, len(videoIDs))
	for _, id := range videoIDs {
		if v, ok := byID[id]; ok {
			ordered = append(ordered, v)
		}
	}
	return ordered, nil
}

func (r *Repository) SearchVideos(ctx context.Context, query, userID string, page domain.Page) ([]domain.Video, error) {
	// Two ways to match, because people search for both.
	//
	// The tsvector covers title and description. Channel name is matched
	// separately with ILIKE rather than being folded into the vector: a channel
	// is not part of the text of a video, and typing one means "show me their
	// videos", which full-text ranking would bury under incidental mentions.
	//
	// websearch_to_tsquery tolerates whatever a user types, unlike to_tsquery.
	// Both sides are folded so "tinh tế" finds "Tinh te" and the reverse.
	return r.queryVideos(ctx, videoSelect+`
		WHERE v.search_tsv @@ websearch_to_tsquery('simple', catalog.immutable_unaccent($2))
		   OR catalog.immutable_unaccent(lower(c.name)) LIKE catalog.immutable_unaccent(lower($5))
		ORDER BY
			ts_rank(v.search_tsv, websearch_to_tsquery('simple', catalog.immutable_unaccent($2))) DESC,
			(v.media_state = 'READY') DESC,
			v.added_at DESC
		LIMIT $3 OFFSET $4`,
		userID, query, page.Size, page.Offset, "%"+query+"%")
}

// Suggest returns topics and channels first, then video titles.
//
// Ordering matters more than cleverness here: a topic or channel is a
// destination that always has results behind it, while a title is a single
// video, so the broader targets belong at the top of the list.
func (r *Repository) Suggest(ctx context.Context, query string, limit int32) ([]domain.Suggestion, error) {
	// Folded on both sides: the library holds Vietnamese with and without
	// diacritics, and people type it both ways.
	pattern := "%" + query + "%"

	rows, err := r.pool.Query(ctx, `
		(
			SELECT topic AS text, 'TOPIC' AS kind, count(*)::int AS video_count, 0 AS rank
			FROM videos, unnest(topics) AS topic
			WHERE catalog.immutable_unaccent(lower(topic)) LIKE catalog.immutable_unaccent(lower($1))
			GROUP BY topic
		)
		UNION ALL
		(
			SELECT c.name, 'CHANNEL', count(v.id)::int, 1
			FROM channels c
			JOIN videos v ON v.channel_id = c.id
			WHERE catalog.immutable_unaccent(lower(c.name)) LIKE catalog.immutable_unaccent(lower($1))
			GROUP BY c.name
		)
		UNION ALL
		(
			SELECT title, 'TITLE', 1, 2
			FROM videos
			WHERE catalog.immutable_unaccent(lower(title)) LIKE catalog.immutable_unaccent(lower($1))
			-- Prefer what the library actually holds: a cached video is
			-- watchable right now, a queued one has to be fetched first.
			ORDER BY (media_state = 'READY') DESC, view_count DESC NULLS LAST
			LIMIT $2
		)
		ORDER BY rank, video_count DESC
		LIMIT $2`, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Suggestion
	for rows.Next() {
		var (
			s    domain.Suggestion
			kind string
			rank int
		)
		if err := rows.Scan(&s.Text, &kind, &s.VideoCount, &rank); err != nil {
			return nil, err
		}
		s.Kind = domain.SuggestionKind(kind)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repository) ListChannelVideos(ctx context.Context, channelID, userID string, page domain.Page) ([]domain.Video, error) {
	return r.queryVideos(ctx, videoSelect+`
		WHERE v.channel_id = $2
		ORDER BY v.published_at DESC
		LIMIT $3 OFFSET $4`,
		userID, channelID, page.Size, page.Offset)
}

func (r *Repository) ListHistory(ctx context.Context, userID string, page domain.Page) ([]domain.Video, error) {
	return r.queryVideos(ctx, videoSelect+`
		WHERE wp.user_id IS NOT NULL
		ORDER BY wp.last_watched_at DESC
		LIMIT $2 OFFSET $3`,
		userID, page.Size, page.Offset)
}

func (r *Repository) ListPinnedVideos(ctx context.Context, userID string, page domain.Page) ([]domain.Video, error) {
	return r.queryVideos(ctx, videoSelect+`
		WHERE v.pinned = true
		ORDER BY v.added_at DESC
		LIMIT $2 OFFSET $3`,
		userID, page.Size, page.Offset)
}

func (r *Repository) GetChannel(ctx context.Context, channelID, userID string) (domain.Channel, int32, error) {
	var (
		c          domain.Channel
		videoCount int32
	)
	err := r.pool.QueryRow(ctx, `
		SELECT c.id, c.name, c.handle, c.avatar_path, c.banner_path, c.subscriber_count, c.verified,
		       (s.user_id IS NOT NULL) AS subscribed,
		       (SELECT count(*) FROM videos v WHERE v.channel_id = c.id) AS video_count
		FROM channels c
		LEFT JOIN subscriptions s ON s.channel_id = c.id AND s.user_id = $1
		WHERE c.id = $2`,
		userID, channelID,
	).Scan(&c.ID, &c.Name, &c.Handle, &c.AvatarPath, &c.BannerPath, &c.SubscriberCount, &c.Verified,
		&c.Subscribed, &videoCount)

	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Channel{}, 0, fmt.Errorf("channel %s: %w", channelID, domain.ErrNotFound)
	}
	return c, videoCount, err
}

func (r *Repository) ListTopics(ctx context.Context, minVideoCount int32) ([]domain.Topic, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT topic, count(*)::int AS video_count
		FROM videos, unnest(topics) AS topic
		GROUP BY topic
		HAVING count(*) >= $1
		ORDER BY video_count DESC, topic ASC`, minVideoCount)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Topic
	for rows.Next() {
		var c domain.Topic
		if err := rows.Scan(&c.Name, &c.VideoCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertChannel is idempotent on the upstream channel id, so re-ingesting a
// video from a known channel refreshes its details rather than failing.
func (r *Repository) UpsertChannel(ctx context.Context, c domain.Channel) (domain.Channel, error) {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO channels (id, name, handle, avatar_path, banner_path, subscriber_count, verified)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE
		-- Every field keeps what it had when the incoming one says nothing.
		--
		-- Artwork was already written this way; the rest was not, so a caller
		-- with only a fragment to contribute — a channel picture discovered
		-- while listing uploads, say — would blank the name, the handle and the
		-- subscriber count of every channel it touched. A partial update should
		-- add what it knows, never erase what it does not.
		SET name = COALESCE(NULLIF(EXCLUDED.name, ''), channels.name),
		    handle = COALESCE(NULLIF(EXCLUDED.handle, ''), channels.handle),
		    avatar_path = COALESCE(NULLIF(EXCLUDED.avatar_path, ''), channels.avatar_path),
		    banner_path = COALESCE(NULLIF(EXCLUDED.banner_path, ''), channels.banner_path),
		    subscriber_count = CASE WHEN EXCLUDED.subscriber_count > 0
		                            THEN EXCLUDED.subscriber_count
		                            ELSE channels.subscriber_count END,
		    verified = channels.verified OR EXCLUDED.verified
		RETURNING id, name, handle, avatar_path, banner_path, subscriber_count, verified`,
		c.ID, c.Name, c.Handle, c.AvatarPath, c.BannerPath, c.SubscriberCount, c.Verified,
	).Scan(&c.ID, &c.Name, &c.Handle, &c.AvatarPath, &c.BannerPath, &c.SubscriberCount, &c.Verified)
	return c, err
}

// ListSubscriptions returns the channels a user follows, most recently
// subscribed first — the order the sidebar shows them in.
func (r *Repository) ListSubscriptions(ctx context.Context, userID string) ([]domain.Channel, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT c.id, c.name, c.handle, c.avatar_path, c.banner_path,
		       c.subscriber_count, c.verified
		FROM subscriptions s
		JOIN channels c ON c.id = s.channel_id
		WHERE s.user_id = $1
		ORDER BY s.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Channel
	for rows.Next() {
		var c domain.Channel
		if err := rows.Scan(&c.ID, &c.Name, &c.Handle, &c.AvatarPath, &c.BannerPath,
			&c.SubscriberCount, &c.Verified); err != nil {
			return nil, err
		}
		c.Subscribed = true
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertVideo preserves fields the ingest worker does not own — media state,
// pinning and access time — so re-running metadata refresh cannot silently
// mark a downloaded video as queued again.
func (r *Repository) UpsertVideo(ctx context.Context, v domain.Video) (domain.Video, error) {
	// pgx encodes a nil slice as NULL, which the NOT NULL array columns reject.
	// An absent list means "no tags", not "unknown".
	if v.Hashtags == nil {
		v.Hashtags = []string{}
	}
	if v.Topics == nil {
		v.Topics = []string{}
	}

	_, err := r.pool.Exec(ctx, `
		INSERT INTO videos (id, title, channel_id, duration_seconds, view_count,
		                    published_at, added_at, thumbnail_path, description,
		                    hashtags, topics, media_state, media_path,
		                    size_bytes, source_url, language)
		VALUES ($1, $2, $3, $4, $5, $6, now(), $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (id) DO UPDATE
		SET title = EXCLUDED.title,
		    channel_id = EXCLUDED.channel_id,
		    -- A later scan that knows less must not erase what an earlier full
		    -- metadata fetch established.
		    --
		    -- duration, description and hashtags were the exception, and this is
		    -- the comment they sat under: the RSS pass over subscribed channels
		    -- runs every five minutes and carries none of the three, so it wrote
		    -- a zero duration and an empty description over every upload of the
		    -- last 48 hours. 37% of videos published inside that window had no
		    -- duration against 6.5% outside it, and 609 of 617 had no
		    -- description at all.
		    --
		    -- A video whose description is genuinely emptied upstream keeps the
		    -- old text here. That is the same trade thumbnail_path makes on the
		    -- line below, and the same way round.
		    duration_seconds = COALESCE(NULLIF(EXCLUDED.duration_seconds, 0), videos.duration_seconds),
		    view_count = COALESCE(EXCLUDED.view_count, videos.view_count),
		    published_at = COALESCE(EXCLUDED.published_at, videos.published_at),
		    thumbnail_path = COALESCE(NULLIF(EXCLUDED.thumbnail_path, ''), videos.thumbnail_path),
		    description = COALESCE(NULLIF(EXCLUDED.description, ''), videos.description),
		    hashtags = CASE WHEN cardinality(EXCLUDED.hashtags) = 0
		                    THEN videos.hashtags ELSE EXCLUDED.hashtags END,
		    -- Topics accumulate: a video discovered under a second topic keeps
		    -- the first one instead of being reassigned.
		    topics = ARRAY(SELECT DISTINCT unnest(videos.topics || EXCLUDED.topics)),
		    source_url = EXCLUDED.source_url,
		    language = COALESCE(NULLIF(EXCLUDED.language, ''), videos.language)`,
		v.ID, v.Title, v.Channel.ID, v.DurationSeconds, nullableCount(v.ViewCount),
		nullableTime(v.PublishedAt),
		v.ThumbnailPath, v.Description, v.Hashtags, v.Topics,
		string(v.MediaState), v.MediaPath, v.SizeBytes, v.SourceURL, v.Language)
	if err != nil {
		return domain.Video{}, err
	}
	return r.GetVideo(ctx, v.ID, "")
}

// SetShort records what YouTube answered for one video.
//
// A plain write with no read first: the answer does not change once given — a
// video does not stop being a Short — so a second write can only be the same
// answer arriving twice.
func (r *Repository) SetShort(ctx context.Context, videoID string, isShort bool) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE videos SET is_short = $2 WHERE id = $1`, videoID, isShort)
	return err
}

// ListUncheckedShorts returns videos nobody has asked about, likeliest first.
//
// The order is the whole of how long the feed stays wrong. The backlog is
// thousands of rows and one answer costs an HTTP request, so asking in an
// arbitrary order means most of the Shorts are still unasked days later —
// measured at 26 found in the first 113 with 8063 to go.
//
// So the ones that could be a Short go first. Every Short confirmed here ran
// between 0 and 152 seconds, against YouTube's own three-minute cap, and 1357
// of the unasked rows are inside that — a few hours of probing rather than a
// day and a half. Unknown durations ride with them, because a confirmed Short
// in this library has duration 0: a flat listing did not carry one.
//
// This orders the work, it does not answer the question. A video over three
// minutes is asked too, just last, so a change to that cap costs a slow week
// rather than a wrong column. Within each group, newest first: a Short reaches
// the feed while it is new.
func (r *Repository) ListUncheckedShorts(ctx context.Context, limit int32) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id
		FROM videos
		WHERE is_short IS NULL
		ORDER BY
		  CASE
		    WHEN duration_seconds BETWEEN 1 AND 180 THEN 0
		    WHEN duration_seconds IS NULL OR duration_seconds = 0 THEN 1
		    ELSE 2
		  END,
		  published_at DESC NULLS LAST
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *Repository) SetMediaState(ctx context.Context, videoID string, state domain.MediaState, mediaPath string, sizeBytes int64, subtitles []domain.SubtitleTrack) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE videos
		SET media_state = $2,
		    media_path = CASE WHEN $3 <> '' THEN $3 ELSE media_path END,
		    -- Cast, or the file's own size decides whether it can be recorded.
		    -- A parameter's type is inferred from where it is first used, and
		    -- $4 is first used against the literal 0 — an int4 — so the whole
		    -- parameter became an int4 and anything over 2.1 GB was rejected
		    -- before the query ran: "3475035755 is greater than maximum value
		    -- for int4", on a download that had already finished. The column
		    -- has always been a bigint; nothing but the inference was wrong.
		    size_bytes = CASE WHEN $4::bigint > 0 THEN $4::bigint ELSE size_bytes END,
		    last_accessed_at = now()
		WHERE id = $1`,
		videoID, string(state), mediaPath, sizeBytes)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("video %s: %w", videoID, domain.ErrNotFound)
	}

	// Replace rather than merge: the track set describes what is on disk right
	// now, and a re-download may produce a different set of languages.
	if len(subtitles) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM subtitles WHERE video_id = $1`, videoID); err != nil {
			return err
		}
		for _, t := range subtitles {
			if _, err := tx.Exec(ctx, `
				INSERT INTO subtitles (video_id, language, label, path, generated)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (video_id, language) DO UPDATE
				SET label = EXCLUDED.label, path = EXCLUDED.path, generated = EXCLUDED.generated`,
				videoID, t.Language, t.Label, t.Path, t.Generated); err != nil {
				return err
			}
		}
	}

	return tx.Commit(ctx)
}

// loadSubtitles attaches caption tracks to already-fetched videos. Done as one
// extra query rather than a join, because the join would multiply every video
// row by its track count for a field most callers ignore.
func (r *Repository) loadSubtitles(ctx context.Context, videos []domain.Video) error {
	ids := make([]string, 0, len(videos))
	for _, v := range videos {
		ids = append(ids, v.ID)
	}
	if len(ids) == 0 {
		return nil
	}

	rows, err := r.pool.Query(ctx, `
		SELECT video_id, language, label, path, generated
		FROM subtitles WHERE video_id = ANY($1)
		ORDER BY generated, language`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()

	byVideo := map[string][]domain.SubtitleTrack{}
	for rows.Next() {
		var (
			videoID string
			t       domain.SubtitleTrack
		)
		if err := rows.Scan(&videoID, &t.Language, &t.Label, &t.Path, &t.Generated); err != nil {
			return err
		}
		byVideo[videoID] = append(byVideo[videoID], t)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range videos {
		videos[i].Subtitles = byVideo[videos[i].ID]
	}
	return nil
}

func (r *Repository) FindBySourceURL(ctx context.Context, sourceURL, userID string) (domain.Video, error) {
	v, err := scanVideo(r.pool.QueryRow(ctx, videoSelect+` WHERE v.source_url = $2`, userID, sourceURL))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Video{}, fmt.Errorf("source %s: %w", sourceURL, domain.ErrNotFound)
	}
	return v, err
}

func (r *Repository) ListVideoFeatures(ctx context.Context, page domain.Page) ([]domain.VideoFeatures, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, channel_id, topics, hashtags, published_at, added_at,
		       duration_seconds, media_state, language, view_count,
		       -- Unknown reads as "not a Short". Ranking must never withhold a
		       -- video over a question the checker has not reached yet.
		       COALESCE(is_short, false)
		FROM videos
		ORDER BY id
		LIMIT $1 OFFSET $2`, page.Size, page.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.VideoFeatures
	for rows.Next() {
		var (
			f           domain.VideoFeatures
			state       string
			publishedAt *time.Time
		)
		var viewCountFeature *int64
		if err := rows.Scan(&f.VideoID, &f.ChannelID, &f.Topics, &f.Hashtags,
			&publishedAt, &f.AddedAt, &f.DurationSeconds, &state, &f.Language, &viewCountFeature,
			&f.IsShort); err != nil {
			return nil, err
		}
		if publishedAt != nil {
			f.PublishedAt = *publishedAt
		}
		if viewCountFeature != nil {
			f.ViewCount = *viewCountFeature
		}
		f.MediaState = domain.MediaState(state)
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *Repository) ListComments(ctx context.Context, videoID string, sort domain.CommentSort, page domain.Page) ([]domain.Comment, int32, error) {
	order := `c.like_count DESC, c.published_at DESC`
	if sort == domain.SortNewest {
		order = `c.published_at DESC`
	}

	// Pinned comments always lead, mirroring the reference UI.
	rows, err := r.pool.Query(ctx, `
		SELECT c.id, c.video_id, c.user_id, c.author_handle, c.author_avatar,
		       c.body, c.published_at, c.like_count, c.pinned_by, c.parent_comment_id
		FROM comments c
		WHERE c.video_id = $1
		ORDER BY (c.pinned_by IS NOT NULL) DESC, `+order, videoID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var (
		roots     []domain.Comment
		repliesBy = map[string][]domain.Comment{}
		total     int32
	)

	for rows.Next() {
		var (
			c        domain.Comment
			parentID *string
		)
		if err := rows.Scan(&c.ID, &c.VideoID, &c.Author.UserID, &c.Author.Handle,
			&c.Author.AvatarPath, &c.Body, &c.PublishedAt, &c.LikeCount, &c.PinnedBy,
			&parentID); err != nil {
			return nil, 0, err
		}
		total++
		if parentID != nil {
			repliesBy[*parentID] = append(repliesBy[*parentID], c)
		} else {
			roots = append(roots, c)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// Only top-level comments are paginated; replies travel with their parent.
	start := int(page.Offset)
	if start > len(roots) {
		start = len(roots)
	}
	end := start + int(page.Size)
	if end > len(roots) {
		end = len(roots)
	}
	pageRoots := roots[start:end]

	for i := range pageRoots {
		pageRoots[i].Replies = repliesBy[pageRoots[i].ID]
	}
	return pageRoots, total, nil
}

func (r *Repository) ImportComments(ctx context.Context, videoID string, comments []domain.ImportComment) (int32, error) {
	if len(comments) == 0 {
		return 0, nil
	}

	// Build the set of IDs that appear in this batch. When a comment's parent
	// is not in the batch, treat it as top-level instead of failing the FK.
	knownIDs := make(map[string]bool, len(comments))
	for _, c := range comments {
		knownIDs[c.ID] = true
	}

	// Insert in rounds so a child never lands before its parent.
	inserted := make(map[string]bool, len(comments))
	var totalImported int32
	for {
		roundImported := int32(0)
		for _, c := range comments {
			if inserted[c.ID] {
				continue
			}

			effectiveParent := c.ParentID
			if effectiveParent != "" && !knownIDs[effectiveParent] {
				effectiveParent = ""
			}
			if effectiveParent != "" && !inserted[effectiveParent] {
				continue
			}

			var parentID *string
			if effectiveParent != "" {
				parentID = &effectiveParent
			}

			tag, err := r.pool.Exec(ctx, `
				INSERT INTO comments (id, video_id, parent_comment_id, user_id, author_handle,
				                      body, published_at, like_count, pinned_by)
				VALUES ($1, $2, $3, NULL, $4, $5, to_timestamp($6), $7, $8)
				ON CONFLICT (id) DO NOTHING`,
				c.ID, videoID, parentID, c.AuthorHandle,
				c.Text, c.PublishedAtUnix, c.LikeCount, c.PinnedBy)
			if err != nil {
				return totalImported, err
			}

			inserted[c.ID] = true
			if tag.RowsAffected() > 0 {
				roundImported++
			}
		}
		totalImported += roundImported
		if roundImported == 0 {
			break
		}
	}
	return totalImported, nil
}
func (r *Repository) CreateComment(ctx context.Context, c domain.Comment, parentID *string) (domain.Comment, error) {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO comments (id, video_id, parent_comment_id, user_id, author_handle,
		                      author_avatar, body, published_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		c.ID, c.VideoID, parentID, c.Author.UserID, c.Author.Handle,
		c.Author.AvatarPath, c.Body, c.PublishedAt)
	if err != nil {
		return domain.Comment{}, err
	}
	return c, nil
}

// RecordWatchProgress also refreshes last_accessed_at: watching a video is what
// protects it from LRU eviction, not merely seeing it in a grid.
func (r *Repository) RecordWatchProgress(ctx context.Context, userID, videoID string, positionSeconds int32, watchedFraction float32) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO watch_progress (user_id, video_id, position_seconds, watched_fraction, last_watched_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (user_id, video_id) DO UPDATE
		SET position_seconds = EXCLUDED.position_seconds,
		    -- Never regress the high-water mark; rewatching must not undo it.
		    watched_fraction = GREATEST(watch_progress.watched_fraction, EXCLUDED.watched_fraction),
		    last_watched_at  = now()`,
		userID, videoID, positionSeconds, watchedFraction); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE videos SET last_accessed_at = now() WHERE id = $1`, videoID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *Repository) SetReaction(ctx context.Context, userID, videoID string, reaction domain.Reaction) (int64, error) {
	var err error
	if reaction == domain.ReactionNone {
		_, err = r.pool.Exec(ctx,
			`DELETE FROM reactions WHERE user_id = $1 AND video_id = $2`, userID, videoID)
	} else {
		_, err = r.pool.Exec(ctx, `
			INSERT INTO reactions (user_id, video_id, reaction)
			VALUES ($1, $2, $3)
			ON CONFLICT (user_id, video_id) DO UPDATE SET reaction = EXCLUDED.reaction`,
			userID, videoID, string(reaction))
	}
	if err != nil {
		return 0, err
	}

	var likes int64
	err = r.pool.QueryRow(ctx,
		`SELECT count(*) FROM reactions WHERE video_id = $1 AND reaction = 'LIKE'`,
		videoID).Scan(&likes)
	return likes, err
}

func (r *Repository) SetSubscription(ctx context.Context, userID, channelID string, subscribed bool) error {
	var err error
	if subscribed {
		_, err = r.pool.Exec(ctx, `
			INSERT INTO subscriptions (user_id, channel_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, userID, channelID)
	} else {
		_, err = r.pool.Exec(ctx,
			`DELETE FROM subscriptions WHERE user_id = $1 AND channel_id = $2`, userID, channelID)
	}
	return err
}

func (r *Repository) GetStorageUsage(ctx context.Context, budgetBytes int64) (domain.StorageUsage, error) {
	usage := domain.StorageUsage{BudgetBytes: budgetBytes}

	err := r.pool.QueryRow(ctx, `
		SELECT coalesce(sum(size_bytes) FILTER (WHERE media_state = 'READY'), 0),
		       count(*)::int,
		       count(*) FILTER (WHERE media_state = 'EVICTED')::int
		FROM videos`).
		Scan(&usage.UsedBytes, &usage.VideoCount, &usage.EvictedCount)
	if err != nil {
		return usage, err
	}

	usage.DiskFreeBytes = diskFreeBytes(r.mediaRoot)

	// Least recently watched unpinned videos are the next to lose their bytes.
	usage.EvictionCandidates, err = r.queryVideos(ctx, videoSelect+`
		WHERE v.media_state = 'READY' AND NOT v.pinned
		ORDER BY v.last_accessed_at ASC
		LIMIT 10`, "")
	return usage, err
}

func (r *Repository) SetPinned(ctx context.Context, videoID string, pinned bool) error {
	tag, err := r.pool.Exec(ctx, `UPDATE videos SET pinned = $2 WHERE id = $1`, videoID, pinned)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("video %s: %w", videoID, domain.ErrNotFound)
	}
	return nil
}

func (r *Repository) UsedBytes(ctx context.Context) (int64, error) {
	var used int64
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(size_bytes), 0) FROM videos WHERE media_state = 'READY'`).Scan(&used)
	return used, err
}

// ListEvictionCandidates returns unpinned downloaded videos, least recently
// accessed first. This is the query the partial index in 0001_init.sql exists
// to serve.
func (r *Repository) ListEvictionCandidates(ctx context.Context, _ int64) ([]domain.EvictionCandidate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, media_path, size_bytes
		FROM videos
		WHERE media_state = 'READY' AND NOT pinned
		ORDER BY last_accessed_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.EvictionCandidate
	for rows.Next() {
		var c domain.EvictionCandidate
		if err := rows.Scan(&c.VideoID, &c.MediaPath, &c.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkEvicted keeps everything except the bytes: the row, the thumbnail and the
// history survive so the video can offer to fetch itself again.
// NormaliseChannelIDs fixes videos whose channel_id is an @handle rather than
// the canonical UC… id. Flat playlist listings sometimes return the handle form,
// and scanning from two different source types (channel vs playlist) creates two
// channel rows for one real channel. Run once at startup; cheap enough to repeat
// because there are few duplicate channels in a library of this size.
func (r *Repository) NormaliseChannelIDs(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
		WITH bad AS (
			SELECT id, name FROM catalog.channels WHERE id LIKE '@%'
		),
		good AS (
			SELECT id, name FROM catalog.channels WHERE id NOT LIKE '@%'
		),
		remap AS (
			SELECT b.id AS old_id, g.id AS new_id
			FROM bad b
			JOIN good g ON LOWER(g.name) = LOWER(b.name)
		)
		UPDATE catalog.videos v
		SET channel_id = r.new_id
		FROM remap r
		WHERE v.channel_id = r.old_id
	`)
	if err != nil {
		return fmt.Errorf("normalise channel ids: update videos: %w", err)
	}

	// Clean up orphaned @handle channels that no longer have any videos.
	_, err = r.pool.Exec(ctx, `
		DELETE FROM catalog.channels
		WHERE id LIKE '@%'
		  AND NOT EXISTS (SELECT 1 FROM catalog.videos WHERE channel_id = channels.id)
	`)
	if err != nil {
		return fmt.Errorf("normalise channel ids: delete orphan channels: %w", err)
	}

	// Strip sqp and rs query parameters from thumbnail URLs. yt-dlp's listings
	// carry them by default, and they sometimes cause YouTube to serve a generic
	// grey placeholder instead of the real still.
	_, err = r.pool.Exec(ctx, `
		UPDATE catalog.videos
		SET thumbnail_path = substring(thumbnail_path from '^[^?]+')
		WHERE thumbnail_path LIKE '%?sqp=%'
		   OR thumbnail_path LIKE '%?rs=%'
	`)
	if err != nil {
		return fmt.Errorf("normalise channel ids: strip thumbnail params: %w", err)
	}
	return nil
}

// DownloadMissingThumbnails fetches thumbnails for videos that still reference
// a remote URL. Each thumbnail is a few kilobytes; the whole pass is cheap and
// ensures the frontend never depends on YouTube's CDN.
func (r *Repository) DownloadMissingThumbnails(ctx context.Context) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, thumbnail_path FROM catalog.videos
		WHERE thumbnail_path LIKE 'https://i.ytimg.com/%'
	`)
	if err != nil {
		return
	}
	defer rows.Close()

	type row struct{ id, url string }
	var videos []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.url); err != nil {
			continue
		}
		videos = append(videos, r)
	}

	thumbDir := filepath.Join(r.mediaRoot, "thumbnails")
	_ = os.MkdirAll(thumbDir, 0o755)

	// Try these in order: hq720 (1280×720 — available for nearly all HD
	// videos), maxresdefault (1920×1080 — rare), sddefault (640×480), then
	// the stored URL (usually hqdefault at 480×360).
	candidates := func(videoID, storedURL string) []string {
		return []string{
			"https://i.ytimg.com/vi/" + videoID + "/hq720.jpg",
			"https://i.ytimg.com/vi/" + videoID + "/maxresdefault.jpg",
			"https://i.ytimg.com/vi/" + videoID + "/sddefault.jpg",
			storedURL,
		}
	}

	// Download four at a time so the whole pass finishes in seconds rather
	// than minutes. Each thumbnail is a few kilobytes; YouTube rate-limiting
	// is the only constraint, and four concurrent fetches is well under it.
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup

	for _, v := range videos {
		wg.Add(1)
		go func(v row) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			downloaded := false
			for _, url := range candidates(v.id, v.url) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				continue
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				continue
			}
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				continue
			}

			dst := filepath.Join(thumbDir, v.id+".jpg")
			file, err := os.Create(dst)
			if err != nil {
				resp.Body.Close()
				continue
			}
			if _, err := io.Copy(file, resp.Body); err != nil {
				file.Close()
				resp.Body.Close()
				continue
			}
			file.Close()
			resp.Body.Close()
			downloaded = true
			break
		}

		if downloaded {
			_, _ = r.pool.Exec(ctx, `
				UPDATE catalog.videos SET thumbnail_path = $1 WHERE id = $2
			`, filepath.Join("thumbnails", v.id+".jpg"), v.id)
		}
	}(v)
	}
	wg.Wait()
}

func (r *Repository) MarkEvicted(ctx context.Context, videoID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE videos
		SET media_state = 'EVICTED', media_path = '', size_bytes = 0
		WHERE id = $1`, videoID)
	return err
}

// nullableTime and nullableCount keep "unknown" distinct from "zero" on the
// way into the database, so a scan that lacks a field cannot overwrite a value
// a full metadata fetch already found.
func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func nullableCount(n int64) *int64 {
	if n <= 0 {
		return nil
	}
	return &n
}

// diskFreeBytes reports free space on the volume holding the media directory.
// Reported as zero when unavailable; the caller treats it as informational.
func diskFreeBytes(path string) int64 {
	if strings.TrimSpace(path) == "" {
		return 0
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return int64(stat.Bavail) * int64(stat.Bsize)
}
