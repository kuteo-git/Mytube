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
       v.media_path, v.size_bytes, (sv.user_id IS NOT NULL) AS pinned, v.source_url,
       c.id, c.name, c.handle, c.avatar_path, c.subscriber_count, c.verified,
       (s.user_id IS NOT NULL) AS subscribed,
       wp.position_seconds, wp.watched_fraction, wp.last_watched_at,
       r.reaction, (wl.user_id IS NOT NULL) AS in_watch_later,
       v.live_status,
       -- The one definition of "on air now". Thirty minutes is three passes of
       -- the ten-minute live scan, so a single failed pass does not put a
       -- broadcast out and a finished one does not stay lit for long.
       -- COALESCE because NULL is the ordinary case, not an edge one: nothing
       -- has asked about all but a few hundred of these rows, and in SQL a
       -- comparison against NULL is NULL rather than false. Without it every video
       -- the live scan has never visited fails to scan into a bool at all —
       -- which is to say every video in the library.
       COALESCE(v.live_status = 'is_live'
                AND v.live_checked_at > now() - interval '30 minutes', false) AS is_live_now,
       (SELECT count(*) FROM reactions lr
         WHERE lr.video_id = v.id AND lr.reaction = 'LIKE') AS like_count
FROM videos v
JOIN channels c ON c.id = v.channel_id
LEFT JOIN subscriptions  s  ON s.channel_id = c.id  AND s.user_id  = $1
LEFT JOIN watch_progress wp ON wp.video_id  = v.id  AND wp.user_id = $1
LEFT JOIN reactions      r  ON r.video_id   = v.id  AND r.user_id  = $1
LEFT JOIN watch_later    wl ON wl.video_id  = v.id  AND wl.user_id = $1
LEFT JOIN saved          sv ON sv.video_id  = v.id  AND sv.user_id = $1
`

// Video.pinned carries "this viewer has saved it", not videos.pinned, which
// after 0014_saved.sql means "somebody has, so the sweep must leave the file
// alone". Only the eviction queries want the second, and they read the column
// directly a few hundred lines below; nothing outside this service has a use
// for a household-wide flag, while every card on the page needs the first to
// label its own button.

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

		// NULL for every row nobody has asked about, which is nearly all of
		// them: the live scan visits subscribed channels only.
		liveStatus *string
		isLiveNow  bool
	)

	err := row.Scan(
		&v.ID, &v.Title, &v.DurationSeconds, &viewCount, &publishedAt, &v.AddedAt,
		&v.ThumbnailPath, &v.Description, &v.Hashtags, &v.Topics, &state,
		&v.MediaPath, &v.SizeBytes, &v.Pinned, &v.SourceURL,
		&v.Channel.ID, &v.Channel.Name, &v.Channel.Handle, &v.Channel.AvatarPath,
		&v.Channel.SubscriberCount, &v.Channel.Verified,
		&subscribed,
		&positionSeconds, &watchedFraction, &lastWatchedAt,
		&reaction, &inWatchLater, &liveStatus, &isLiveNow, &v.LikeCount,
	)
	if err != nil {
		return domain.Video{}, err
	}

	v.MediaState = domain.MediaState(state)
	v.Channel.Subscribed = subscribed
	if liveStatus != nil {
		v.LiveStatus = *liveStatus
	}
	v.IsLiveNow = isLiveNow
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
	// One member's shelf, newest save first — not the video's added_at, which
	// ordered the page by when the library got the video rather than by when
	// this viewer put it there.
	return r.queryVideos(ctx, videoSelect+`
		WHERE sv.user_id IS NOT NULL
		ORDER BY sv.created_at DESC
		LIMIT $2 OFFSET $3`,
		userID, page.Size, page.Offset)
}

func (r *Repository) GetChannel(ctx context.Context, channelID, userID string) (domain.Channel, int32, error) {
	return r.channelWhere(ctx, "c.id = $2", channelID, userID)
}

// GetChannelByHandle answers the same question from the other name.
//
// A pasted channel address usually carries a handle rather than an id, and 1626
// of this library's 1690 channels have one — so this is what keeps opening such
// an address from costing an upstream request against the address §8 risk 6 is
// about. Case-insensitively, because a handle is written however the person
// pasting it happened to see it.
func (r *Repository) GetChannelByHandle(ctx context.Context, handle, userID string) (domain.Channel, int32, error) {
	return r.channelWhere(ctx, "lower(c.handle) = lower($2)", handle, userID)
}

// One query, two ways in. Written once because the two differ by a single
// predicate, and the columns — nine of them, one a correlated count — are
// exactly the sort of thing that drifts when it is copied.
func (r *Repository) channelWhere(ctx context.Context, predicate, key, userID string) (domain.Channel, int32, error) {
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
		WHERE `+predicate,
		userID, key,
	).Scan(&c.ID, &c.Name, &c.Handle, &c.AvatarPath, &c.BannerPath, &c.SubscriberCount, &c.Verified,
		&c.Subscribed, &videoCount)

	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Channel{}, 0, fmt.Errorf("channel %s: %w", key, domain.ErrNotFound)
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
// ListAllSubscribedChannels is every member's subscriptions with the duplicates
// removed — one row per channel, however many people follow it.
//
// The scanner's question, and a different one from ListSubscriptions': a channel
// is worth reading for new uploads because somebody in the house follows it.
// Asking as one member left every channel only the others follow unscanned.
func (r *Repository) ListAllSubscribedChannels(ctx context.Context) ([]domain.Channel, error) {
	return r.queryChannels(ctx, `
		SELECT DISTINCT ON (c.id)
		       c.id, c.name, c.handle, c.avatar_path, c.banner_path,
		       c.subscriber_count, c.verified
		FROM subscriptions s
		JOIN channels c ON c.id = s.channel_id
		ORDER BY c.id`)
}

func (r *Repository) ListSubscriptions(ctx context.Context, userID string) ([]domain.Channel, error) {
	return r.queryChannels(ctx, `
		SELECT c.id, c.name, c.handle, c.avatar_path, c.banner_path,
		       c.subscriber_count, c.verified
		FROM subscriptions s
		JOIN channels c ON c.id = s.channel_id
		WHERE s.user_id = $1
		ORDER BY s.created_at DESC`, userID)
}

// queryChannels runs any query returning the seven channel columns, in that
// order. Every row it yields came out of subscriptions, so Subscribed is true.
func (r *Repository) queryChannels(ctx context.Context, query string, args ...any) ([]domain.Channel, error) {
	rows, err := r.pool.Query(ctx, query, args...)
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
		                    size_bytes, source_url, language, discovered_via,
		                    live_status, live_checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, now(), $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
		        NULLIF($17, ''), CASE WHEN $17 <> '' THEN now() END)
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
		    language = COALESCE(NULLIF(EXCLUDED.language, ''), videos.language),
		    -- First writer wins. How a video reached the library is a fact about
		    -- the moment it arrived, and a later scan finding it again under a
		    -- curated source does not change where it came from — nor should a
		    -- related lookup be able to relabel something the viewer chose.
		    discovered_via = COALESCE(videos.discovered_via, NULLIF(EXCLUDED.discovered_via, '')),
		    -- The one field here where a *quieter* answer must win.
		    --
		    -- Every rule above protects what an earlier, better-informed fetch
		    -- established, because those facts do not change. This one does:
		    -- "was_live" arriving over "is_live" is the broadcast ending, which
		    -- is the single most important thing this column has to record. A
		    -- COALESCE in the usual direction would leave the red dot lit for
		    -- ever.
		    --
		    -- Silence is still not an answer. An upsert carrying no live_status
		    -- — every ordinary scan, every RSS pass — leaves both columns alone
		    -- rather than erasing what the live scan found a minute ago.
		    live_status = COALESCE(NULLIF(EXCLUDED.live_status, ''), videos.live_status),
		    live_checked_at = CASE WHEN EXCLUDED.live_status IS NOT NULL
		                           THEN EXCLUDED.live_checked_at
		                           ELSE videos.live_checked_at END`,
		v.ID, v.Title, v.Channel.ID, v.DurationSeconds, nullableCount(v.ViewCount),
		nullableTime(v.PublishedAt),
		v.ThumbnailPath, v.Description, v.Hashtags, v.Topics,
		string(v.MediaState), v.MediaPath, v.SizeBytes, v.SourceURL, v.Language,
		nullableText(v.DiscoveredVia), v.LiveStatus)
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

// ListLive returns the broadcasts on air now, from channels this member
// follows.
//
// Ordered newest-confirmed first rather than by any score. "Everything on air"
// is a list, and the handful of rows involved gives ranking nothing to sort;
// the diversity cap would only start hiding channels from a set small enough to
// show whole.
//
// Filtered by subscription rather than shown household-wide. The library is
// shared, but who you follow is not: one member follows 8 channels and another
// 152, and an unfiltered list would be almost entirely somebody else's.
func (r *Repository) ListLive(ctx context.Context, userID string) ([]domain.Video, error) {
	rows, err := r.pool.Query(ctx, videoSelect+`
		JOIN subscriptions live_s
		  ON live_s.channel_id = v.channel_id AND live_s.user_id = $1
		WHERE v.live_status = 'is_live'
		  AND v.live_checked_at > now() - interval '30 minutes'
		ORDER BY v.live_checked_at DESC, v.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.Video, 0, 16)
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repository) ListVideoFeatures(ctx context.Context, page domain.Page) ([]domain.VideoFeatures, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, channel_id, topics, hashtags, published_at, added_at,
		       duration_seconds, media_state, language, view_count,
		       -- Unknown reads as "not a Short". Ranking must never withhold a
		       -- video over a question the checker has not reached yet.
		       COALESCE(is_short, false),
		       discovered_via,
		       -- On air *now*, not merely once. Ranking exempts a live row from
		       -- the 365-day age filter, and a broadcast that ended last year
		       -- must not keep that exemption.
		       COALESCE(live_status = 'is_live'
		                AND live_checked_at > now() - interval '30 minutes', false) AS is_live
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
		var discoveredVia *string
		if err := rows.Scan(&f.VideoID, &f.ChannelID, &f.Topics, &f.Hashtags,
			&publishedAt, &f.AddedAt, &f.DurationSeconds, &state, &f.Language, &viewCountFeature,
			&f.IsShort, &discoveredVia, &f.IsLive); err != nil {
			return nil, err
		}
		if discoveredVia != nil {
			f.DiscoveredVia = *discoveredVia
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

// ImportWatchLater makes the member's Watch later match what the import found.
//
// Same rule as ImportPlaylistItems, and for the same reason: the list cannot be
// edited in this app, so it has to follow upstream — but only as far as the read
// actually saw. A truncated read adds and removes nothing; an empty one is
// treated as a refusal rather than as an emptied list.
func (r *Repository) ImportWatchLater(
	ctx context.Context, userID string, videoIDs []string, complete bool,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if complete && len(videoIDs) > 0 {
		if _, err := tx.Exec(ctx, `
			DELETE FROM watch_later
			WHERE user_id = $1 AND video_id <> ALL($2::text[])`,
			userID, videoIDs); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO watch_later (user_id, video_id)
		SELECT $1, w.video_id
		FROM unnest($2::text[]) AS w(video_id)
		JOIN videos v ON v.id = w.video_id
		ON CONFLICT DO NOTHING`, userID, videoIDs); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ListWatchLater is one viewer's list, oldest addition last.
//
// Newest first, like the Saved shelf: a list read to decide what to watch next
// is read from the top, and the top is what was just put there.
func (r *Repository) ListWatchLater(ctx context.Context, userID string, page domain.Page) ([]domain.Video, error) {
	return r.queryVideos(ctx, videoSelect+`
		WHERE wl.user_id IS NOT NULL
		ORDER BY wl.created_at DESC
		LIMIT $2 OFFSET $3`,
		userID, page.Size, page.Offset)
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
		       count(*) FILTER (WHERE media_state = 'EVICTED')::int,
		       count(*) FILTER (WHERE media_state = 'READY' AND pinned)::int
		FROM videos`).
		Scan(&usage.UsedBytes, &usage.VideoCount, &usage.EvictedCount, &usage.KeptCount)
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

// SetPinned puts a video on one member's shelf, or takes it off, and recomputes
// whether the eviction sweep may touch its bytes.
//
// Two writes rather than one because they answer two questions (0014_saved.sql).
// videos.pinned is derived from the shelf and never set directly: a member
// unsaving must not expose a file another member is keeping, and computing it
// from the table rather than from the direction of this call means the two can
// never drift.
//
// In one transaction so a crash between them cannot leave a pinned video nobody
// has saved — which nothing would ever clear, since only a save or an unsave of
// that same video recomputes it.
func (r *Repository) SetPinned(ctx context.Context, userID, videoID string, pinned bool) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Asked first, and taking the row, so an unknown video is a 404 rather than
	// a foreign-key violation from the insert below — which the caller can only
	// report as a fault of this service.
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT true FROM videos WHERE id = $1 FOR UPDATE`, videoID).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("video %s: %w", videoID, domain.ErrNotFound)
		}
		return err
	}

	if pinned {
		_, err = tx.Exec(ctx, `
			INSERT INTO saved (user_id, video_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, userID, videoID)
	} else {
		_, err = tx.Exec(ctx,
			`DELETE FROM saved WHERE user_id = $1 AND video_id = $2`, userID, videoID)
	}
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE videos v SET pinned = EXISTS (SELECT 1 FROM saved s WHERE s.video_id = v.id)
		WHERE v.id = $1`, videoID); err != nil {
		return err
	}
	return tx.Commit(ctx)
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

// nullableText writes NULL for the empty string.
//
// The column is a tri-state and "" is not one of its three values: the CHECK
// allows SOURCE, RELATED, SEARCH or NULL. It also keeps the first-writer-wins
// COALESCE in UpsertVideo honest — an empty string from a caller that does not
// know the provenance must not overwrite one that did.
func nullableText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
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

// ---------------------------------------------------------------------------
// Playlists
// ---------------------------------------------------------------------------

// playlistSelect carries the count and the first few thumbnails with the row,
// so the playlists page is one query rather than one per playlist.
const playlistSelect = `
SELECT p.id, p.user_id, p.title, p.description, p.source_url, p.updated_at,
       (p.items_synced_at IS NOT NULL), p.unavailable,
       (SELECT count(*) FROM playlist_items i WHERE i.playlist_id = p.id)::int,
       COALESCE((
         SELECT array_agg(t.thumbnail_path ORDER BY t.position)
         FROM (
           SELECT v.thumbnail_path, i.position
           FROM playlist_items i
           JOIN videos v ON v.id = i.video_id
           WHERE i.playlist_id = p.id AND v.thumbnail_path <> ''
           ORDER BY i.position
           LIMIT 4
         ) t
       ), '{}')
FROM playlists p
`

func scanPlaylist(row pgx.Row) (domain.Playlist, error) {
	var (
		p         domain.Playlist
		sourceURL *string
	)
	err := row.Scan(&p.ID, &p.UserID, &p.Title, &p.Description, &sourceURL,
		&p.UpdatedAt, &p.ItemsSynced, &p.Unavailable, &p.ItemCount, &p.ThumbnailPaths)
	if sourceURL != nil {
		p.SourceURL = *sourceURL
	}
	return p, err
}

func (r *Repository) ListPlaylists(ctx context.Context, userID, videoID string) ([]domain.Playlist, error) {
	rows, err := r.pool.Query(ctx, playlistSelect+`
		WHERE p.user_id = $1
		ORDER BY p.updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Playlist
	for rows.Next() {
		p, err := scanPlaylist(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if videoID == "" {
		return out, nil
	}
	holding, err := r.playlistIDsContaining(ctx, userID, videoID)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].ContainsVideo = holding[out[i].ID]
	}
	return out, nil
}

// playlistIDsContaining answers "which of this member's lists hold this video"
// in one query. Reading each playlist's contents instead would be one query per
// playlist for a single bit each.
func (r *Repository) playlistIDsContaining(ctx context.Context, userID, videoID string) (map[string]bool, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT i.playlist_id
		FROM playlist_items i
		JOIN playlists p ON p.id = i.playlist_id
		WHERE p.user_id = $1 AND i.video_id = $2`, userID, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// AddPlaylistItem appends one video to a playlist the member owns.
//
// The position is computed inside the INSERT rather than read and then written:
// two clients adding at once would otherwise pick the same number. Ownership is
// part of the statement for the reason GetPlaylist's is — a check written
// separately from the query is one the next call site forgets.
//
// The rows come FROM playlists, not from playlist_items with an EXISTS beside
// the aggregate. Measured: that shape inserts into somebody else's playlist,
// because an aggregate SELECT with no GROUP BY returns one row however the
// WHERE went — MAX of nothing is NULL, position 0, and the guard never fires.
// Selecting from the ownership check itself is what makes "not yours" mean no
// row at all.
func (r *Repository) AddPlaylistItem(ctx context.Context, playlistID, userID, videoID string) error {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO playlist_items (playlist_id, video_id, position)
		SELECT $1, $3, (
		  SELECT COALESCE(MAX(i.position), -1) + 1
		  FROM playlist_items i WHERE i.playlist_id = $1
		)
		FROM playlists p
		WHERE p.id = $1 AND p.user_id = $2
		ON CONFLICT (playlist_id, video_id) DO NOTHING`,
		playlistID, userID, videoID)
	if err != nil {
		return err
	}
	// Nothing inserted is either "already there" or "not this member's list",
	// and those must not answer the same way. Ask which.
	if tag.RowsAffected() == 0 {
		if err := r.requirePlaylist(ctx, playlistID, userID); err != nil {
			return err
		}
		return nil
	}
	return r.touchPlaylist(ctx, playlistID, userID)
}

func (r *Repository) RemovePlaylistItem(ctx context.Context, playlistID, userID, videoID string) error {
	if err := r.requirePlaylist(ctx, playlistID, userID); err != nil {
		return err
	}
	_, err := r.pool.Exec(ctx, `
		DELETE FROM playlist_items i
		USING playlists p
		WHERE p.id = i.playlist_id
		  AND i.playlist_id = $1 AND p.user_id = $2 AND i.video_id = $3`,
		playlistID, userID, videoID)
	if err != nil {
		return err
	}
	return r.touchPlaylist(ctx, playlistID, userID)
}

func (r *Repository) UpdatePlaylist(
	ctx context.Context, playlistID, userID, title, description string,
) (domain.Playlist, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE playlists SET title = $3, description = $4, updated_at = now()
		WHERE id = $1 AND user_id = $2`, playlistID, userID, title, description)
	if err != nil {
		return domain.Playlist{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.Playlist{}, fmt.Errorf("playlist %s: %w", playlistID, domain.ErrNotFound)
	}
	return r.GetPlaylist(ctx, playlistID, userID)
}

func (r *Repository) DeletePlaylist(ctx context.Context, playlistID, userID string) error {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM playlists WHERE id = $1 AND user_id = $2`, playlistID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("playlist %s: %w", playlistID, domain.ErrNotFound)
	}
	return nil
}

// requirePlaylist turns "not this member's" into not found, so the id alone
// never reveals that somebody else's playlist exists.
func (r *Repository) requirePlaylist(ctx context.Context, playlistID, userID string) error {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM playlists WHERE id = $1 AND user_id = $2)`,
		playlistID, userID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("playlist %s: %w", playlistID, domain.ErrNotFound)
	}
	return nil
}

func (r *Repository) touchPlaylist(ctx context.Context, playlistID, userID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE playlists SET updated_at = now() WHERE id = $1 AND user_id = $2`,
		playlistID, userID)
	return err
}

// GetPlaylist reads one playlist, scoped to its owner.
//
// The user_id is part of the lookup rather than checked afterwards: a playlist
// belonging to somebody else must be indistinguishable from one that does not
// exist, and a check written separately from the query is a check that can be
// forgotten at the next call site.
func (r *Repository) GetPlaylist(ctx context.Context, playlistID, userID string) (domain.Playlist, error) {
	p, err := scanPlaylist(r.pool.QueryRow(ctx, playlistSelect+`
		WHERE p.id = $1 AND p.user_id = $2`, playlistID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Playlist{}, fmt.Errorf("playlist %s: %w", playlistID, domain.ErrNotFound)
	}
	return p, err
}

func (r *Repository) ListPlaylistVideos(
	ctx context.Context, playlistID, userID string, page domain.Page,
) ([]domain.Video, error) {
	return r.queryVideos(ctx, videoSelect+`
		JOIN playlist_items pi ON pi.video_id = v.id
		JOIN playlists      pl ON pl.id = pi.playlist_id
		WHERE pi.playlist_id = $2 AND pl.user_id = $1
		ORDER BY pi.position
		LIMIT $3 OFFSET $4`,
		userID, playlistID, page.Size, page.Offset)
}

func (r *Repository) CreatePlaylist(ctx context.Context, p domain.Playlist) (domain.Playlist, error) {
	// An imported playlist that is already here is updated rather than doubled.
	// Re-importing is the ordinary case — the account scan runs hourly — and a
	// second copy per pass is what makes that unbearable.
	_, err := r.pool.Exec(ctx, `
		INSERT INTO playlists (id, user_id, title, description, source_url)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))
		ON CONFLICT (user_id, source_url) WHERE source_url IS NOT NULL
		DO UPDATE SET title = EXCLUDED.title,
		              description = EXCLUDED.description,
		              updated_at = now()`,
		p.ID, p.UserID, p.Title, p.Description, p.SourceURL)
	if err != nil {
		return domain.Playlist{}, err
	}
	if p.SourceURL != "" {
		return r.playlistBySource(ctx, p.UserID, p.SourceURL)
	}
	return r.GetPlaylist(ctx, p.ID, p.UserID)
}

func (r *Repository) playlistBySource(ctx context.Context, userID, sourceURL string) (domain.Playlist, error) {
	p, err := scanPlaylist(r.pool.QueryRow(ctx, playlistSelect+`
		WHERE p.user_id = $1 AND p.source_url = $2`, userID, sourceURL))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Playlist{}, fmt.Errorf("playlist %s: %w", sourceURL, domain.ErrNotFound)
	}
	return p, err
}

// ImportPlaylistItems makes the playlist match what the account import found,
// and stamps items_synced_at.
//
// A playlist here is a mirror of the member's YouTube playlist and cannot be
// edited in this app, so a video removed upstream has to go — otherwise the two
// drift and there is no way to correct it.
//
// **But only when the whole playlist was read.** The read is bounded, so a long
// list comes back truncated, and mirroring a truncated read would delete
// everything past the cap. `complete` says which happened: a complete read
// replaces the contents, a truncated one only adds. Neither ever removes a video
// the read did not cover.
//
// Positions come from the order given when the read was complete, so the
// playlist matches upstream exactly; an incomplete read appends from the current
// maximum and leaves what is there alone.
func (r *Repository) ImportPlaylistItems(
	ctx context.Context, playlistID, userID string, videoIDs []string, complete bool,
) (int32, error) {
	if _, err := r.GetPlaylist(ctx, playlistID, userID); err != nil {
		return 0, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// One statement per playlist rather than per video: the position has to be
	// assigned in the order given, and unnest WITH ORDINALITY is what carries
	// that order into SQL. Videos the library does not hold are skipped by the
	// join rather than raising a foreign-key error — the import walks a whole
	// playlist, and one unavailable video must not abandon the rest.
	// The mirror's removal half. Confined to complete reads, and skipped
	// entirely for an empty one: a playlist that answers with nothing is far
	// likelier to be a refusal than a list somebody emptied.
	if complete && len(videoIDs) > 0 {
		if _, err := tx.Exec(ctx, `
			DELETE FROM playlist_items
			WHERE playlist_id = $1 AND video_id <> ALL($2::text[])`,
			playlistID, videoIDs); err != nil {
			return 0, err
		}
		// Positions are rewritten from the read, so the order here is upstream's
		// order rather than the order things happened to arrive in.
		if _, err := tx.Exec(ctx, `
			UPDATE playlist_items i SET position = w.ord
			FROM unnest($2::text[]) WITH ORDINALITY AS w(video_id, ord)
			WHERE i.playlist_id = $1 AND i.video_id = w.video_id`,
			playlistID, videoIDs); err != nil {
			return 0, err
		}
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO playlist_items (playlist_id, video_id, position)
		SELECT $1, w.video_id,
		       COALESCE((SELECT max(position) FROM playlist_items WHERE playlist_id = $1), 0)
		         + row_number() OVER (ORDER BY w.ord)
		FROM unnest($2::text[]) WITH ORDINALITY AS w(video_id, ord)
		JOIN videos v ON v.id = w.video_id
		WHERE NOT EXISTS (
			SELECT 1 FROM playlist_items existing
			WHERE existing.playlist_id = $1 AND existing.video_id = w.video_id
		)
		ON CONFLICT DO NOTHING`, playlistID, videoIDs)
	if err != nil {
		return 0, err
	}

	// Stamped whether or not anything was added: the question it answers is
	// "when did I last look", and a pass that found nothing new still looked.
	if _, err := tx.Exec(ctx,
		`UPDATE playlists SET items_synced_at = now() WHERE id = $1`, playlistID); err != nil {
		return 0, err
	}
	return int32(tag.RowsAffected()), tx.Commit(ctx)
}

// PruneImportedPlaylists removes imported playlists the member no longer has.
//
// The other half of the mirror. Without it a playlist deleted on YouTube stays
// here for ever, because nothing in this app can delete one — which is the trap
// a read-only mirror sets for itself.
//
// Refuses an empty list rather than obeying it, the same guard the item mirror
// uses: an account that answers with no playlists is far likelier to be a
// refusal than an account somebody emptied. Locally made playlists no longer
// exist, but any that predate this are left alone: source_url IS NULL is not
// something upstream can speak about.
func (r *Repository) PruneImportedPlaylists(
	ctx context.Context, userID string, keepSourceURLs []string,
) (int32, error) {
	if len(keepSourceURLs) == 0 {
		return 0, nil
	}
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM playlists
		WHERE user_id = $1 AND source_url IS NOT NULL
		  AND source_url <> ALL($2::text[])`, userID, keepSourceURLs)
	if err != nil {
		return 0, err
	}
	return int32(tag.RowsAffected()), nil
}

// ListUnreadPlaylists is the playlists whose contents have never been read.
//
// Separate from ListStalePlaylists because the two answer different questions
// and carry different budgets. Filling the library happens once and an unread
// playlist is an empty page with a title; re-reading is the hourly cost, and
// that is where a small number belongs. One query each, so neither can quietly
// return the other's rows.
func (r *Repository) ListUnreadPlaylists(ctx context.Context, limit int32) ([]domain.StalePlaylist, error) {
	return r.queryStalePlaylists(ctx, `
		SELECT id, user_id, source_url
		FROM playlists
		WHERE source_url IS NOT NULL AND items_synced_at IS NULL AND NOT unavailable
		ORDER BY created_at
		LIMIT $1`, limit)
}

// ListStalePlaylists is the already-read playlists, longest ago first.
func (r *Repository) ListStalePlaylists(ctx context.Context, limit int32) ([]domain.StalePlaylist, error) {
	return r.queryStalePlaylists(ctx, `
		SELECT id, user_id, source_url
		FROM playlists
		WHERE source_url IS NOT NULL AND items_synced_at IS NOT NULL AND NOT unavailable
		ORDER BY items_synced_at ASC
		LIMIT $1`, limit)
}

// MarkPlaylistUnavailable records that upstream lists this playlist but will
// not hand it over.
//
// Asked once and remembered. Without it each refusal costs a request every pass
// for ever, and sits at the front of the unread queue ahead of playlists that
// could have been read.
func (r *Repository) MarkPlaylistUnavailable(ctx context.Context, playlistID, userID string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE playlists SET unavailable = true, items_synced_at = now()
		WHERE id = $1 AND user_id = $2`, playlistID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("playlist %s: %w", playlistID, domain.ErrNotFound)
	}
	return nil
}

func (r *Repository) queryStalePlaylists(ctx context.Context, query string, limit int32) ([]domain.StalePlaylist, error) {
	rows, err := r.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.StalePlaylist
	for rows.Next() {
		var p domain.StalePlaylist
		if err := rows.Scan(&p.ID, &p.UserID, &p.SourceURL); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteUserData removes everything keyed to one member, or counts it without
// removing anything when dryRun is set.
//
// One transaction, and one list of tables. A half-finished delete is worse than
// none — it leaves a member who is gone from the picker and still present in
// the ranker — and two lists, one for counting and one for deleting, would be
// two definitions of "what belongs to this profile" that agree until the day
// they do not.
//
// `videos` and `channels` are absent on purpose. They carry no user_id: the
// library is the household's, and `videos.channel_id -> channels ON DELETE
// CASCADE` would turn removing one channel into removing every video of it,
// with every other member's history, reactions and playlists going down with
// them. 621 of this library's 708 channels arrived through ExpandLibrary rather
// than anyone's subscription, so "nobody is subscribed" is not "nobody wants
// it".
//
// `playlist_items` is absent for the opposite reason: it is keyed by playlist,
// and `playlist_items.playlist_id -> playlists ON DELETE CASCADE` already takes
// it.
func (r *Repository) DeleteUserData(
	ctx context.Context, userID string, dryRun bool,
) (domain.UserDataCounts, error) {
	var counts domain.UserDataCounts

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return counts, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Counted first either way, so a dry run and a real one answer with the
	// same numbers for the same member — the dialog shows these and then the
	// delete reports them back.
	tables := []struct {
		name  string
		into  *int64
	}{
		{"subscriptions", &counts.Subscriptions},
		{"watch_progress", &counts.WatchProgress},
		{"reactions", &counts.Reactions},
		{"saved", &counts.Saved},
		{"watch_later", &counts.WatchLater},
		{"playlists", &counts.Playlists},
		{"comments", &counts.Comments},
	}
	for _, t := range tables {
		// The table name is a literal from the slice above, never from a
		// caller: there is nothing here for a parameter to carry.
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM `+t.name+` WHERE user_id = $1`, userID).Scan(t.into); err != nil {
			return domain.UserDataCounts{}, err
		}
	}

	if dryRun {
		// Rolled back by the deferred call. Nothing was written, and the counts
		// were read inside the same snapshot the real run would have used.
		return counts, nil
	}

	for _, t := range tables {
		if _, err := tx.Exec(ctx,
			`DELETE FROM `+t.name+` WHERE user_id = $1`, userID); err != nil {
			return domain.UserDataCounts{}, err
		}
	}

	// `pinned` is derived from `saved` (§6b), and the rows that derived it have
	// just gone. Without this, a video kept only because this member saved it
	// stays pinned for ever — invisible to the eviction sweep, holding disk
	// against a 300 GiB budget with nobody left who wanted it.
	//
	// Narrowed to videos that were pinned, so this is not a full-table update on
	// every profile deletion.
	if _, err := tx.Exec(ctx, `
		UPDATE videos v SET pinned = EXISTS (SELECT 1 FROM saved s WHERE s.video_id = v.id)
		WHERE v.pinned`); err != nil {
		return domain.UserDataCounts{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.UserDataCounts{}, err
	}
	return counts, nil
}
