// Package usecase holds the catalog application logic: validation, defaults
// and orchestration. It depends on domain ports only.
package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/lucnguyen/local-youtube/services/catalog/internal/domain"
)

const (
	defaultPageSize = 24
	maxPageSize     = 100
	maxBatchSize    = 200
	// How many videos one Short-check pass may ask about.
	//
	// Matches the metadata backfill's 200. Each answer is an HTTP request to
	// YouTube, and §8 of the charter is about exactly this: a pass is always
	// bounded, and a zero limit means this number rather than "all of them".
	maxShortCheckBatch = 200
	// How many playlists one importer pass may be handed. Each is a request to
	// YouTube on a credentialed session, and there are 30 on this installation:
	// a pass that walked them all would put thirty named requests an hour
	// against the address §8's risk 6 is about.
	maxStalePlaylists = 25
)

type Catalog struct {
	repo        domain.Repository
	budgetBytes int64
	now         func() time.Time
	newID       func() string
}

func NewCatalog(repo domain.Repository, budgetBytes int64) *Catalog {
	return &Catalog{
		repo:        repo,
		budgetBytes: budgetBytes,
		now:         time.Now,
		newID:       func() string { return uuid.NewString() },
	}
}

func clampPage(size, offset int32) domain.Page {
	if size <= 0 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}
	if offset < 0 {
		offset = 0
	}
	return domain.Page{Size: size, Offset: offset}
}

func (c *Catalog) GetVideo(ctx context.Context, videoID, userID string) (domain.Video, error) {
	if videoID == "" {
		return domain.Video{}, fmt.Errorf("%w: video_id is required", domain.ErrInvalid)
	}
	return c.repo.GetVideo(ctx, videoID, userID)
}

func (c *Catalog) BatchGetVideos(ctx context.Context, ids []string, userID string) ([]domain.Video, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > maxBatchSize {
		return nil, fmt.Errorf("%w: at most %d ids per batch", domain.ErrInvalid, maxBatchSize)
	}
	return c.repo.BatchGetVideos(ctx, ids, userID)
}

func (c *Catalog) SearchVideos(ctx context.Context, query, userID string, size, offset int32) ([]domain.Video, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	return c.repo.SearchVideos(ctx, query, userID, clampPage(size, offset))
}

// Suggest powers type-ahead. It stays deliberately cheap: a couple of ILIKE
// scans over a few thousand rows costs under a millisecond, and the query runs
// on every keystroke after a debounce.
func (c *Catalog) Suggest(ctx context.Context, query string, limit int32) ([]domain.Suggestion, error) {
	query = strings.TrimSpace(query)
	// Below three characters almost everything matches, which is noise rather
	// than help.
	if len([]rune(query)) < 3 {
		return nil, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	return c.repo.Suggest(ctx, query, limit)
}

func (c *Catalog) ListChannelVideos(ctx context.Context, channelID, userID string, size, offset int32) ([]domain.Video, error) {
	if channelID == "" {
		return nil, fmt.Errorf("%w: channel_id is required", domain.ErrInvalid)
	}
	return c.repo.ListChannelVideos(ctx, channelID, userID, clampPage(size, offset))
}

func (c *Catalog) GetChannel(ctx context.Context, channelID, userID string) (domain.Channel, int32, error) {
	if channelID == "" {
		return domain.Channel{}, 0, fmt.Errorf("%w: channel_id is required", domain.ErrInvalid)
	}
	return c.repo.GetChannel(ctx, channelID, userID)
}

// ListTopics powers the home chip bar and the sidebar. A minimum count of one
// keeps the UI from offering a filter that would produce an empty grid.
func (c *Catalog) ListTopics(ctx context.Context, minVideoCount int32) ([]domain.Topic, error) {
	if minVideoCount <= 0 {
		minVideoCount = 1
	}
	return c.repo.ListTopics(ctx, minVideoCount)
}

// ListVideoFeatures serves the recommendation service. The page cap is higher
// than the UI default because recsys pulls the whole library in a few calls.
func (c *Catalog) ListVideoFeatures(ctx context.Context, size, offset int32) ([]domain.VideoFeatures, error) {
	if size <= 0 || size > 1000 {
		size = 1000
	}
	if offset < 0 {
		offset = 0
	}
	return c.repo.ListVideoFeatures(ctx, domain.Page{Size: size, Offset: offset})
}

func (c *Catalog) UpsertChannel(ctx context.Context, ch domain.Channel) (domain.Channel, error) {
	if ch.ID == "" || strings.TrimSpace(ch.Name) == "" {
		return domain.Channel{}, fmt.Errorf("%w: channel id and name are required", domain.ErrInvalid)
	}
	return c.repo.UpsertChannel(ctx, ch)
}

func (c *Catalog) UpsertVideo(ctx context.Context, v domain.Video) (domain.Video, error) {
	switch {
	case v.ID == "":
		return domain.Video{}, fmt.Errorf("%w: video id is required", domain.ErrInvalid)
	case strings.TrimSpace(v.Title) == "":
		return domain.Video{}, fmt.Errorf("%w: title is required", domain.ErrInvalid)
	case v.Channel.ID == "":
		return domain.Video{}, fmt.Errorf("%w: channel id is required", domain.ErrInvalid)
	case v.SourceURL == "":
		return domain.Video{}, fmt.Errorf("%w: source url is required", domain.ErrInvalid)
	}
	if v.MediaState == "" {
		v.MediaState = domain.MediaQueued
	}
	// PublishedAt stays zero when the caller does not know it; the repository
	// stores NULL rather than inventing a date.
	return c.repo.UpsertVideo(ctx, v)
}

func (c *Catalog) SetMediaState(ctx context.Context, videoID string, state domain.MediaState, mediaPath string, sizeBytes int64, subtitles []domain.SubtitleTrack) error {
	if videoID == "" {
		return fmt.Errorf("%w: video_id is required", domain.ErrInvalid)
	}
	switch state {
	case domain.MediaQueued, domain.MediaDownloading, domain.MediaReady,
		domain.MediaEvicted, domain.MediaFailed, domain.MediaUnavailable:
	default:
		return fmt.Errorf("%w: unknown media state %q", domain.ErrInvalid, state)
	}
	return c.repo.SetMediaState(ctx, videoID, state, mediaPath, sizeBytes, subtitles)
}

// SetShort records YouTube's answer about one video.
func (c *Catalog) SetShort(ctx context.Context, videoID string, isShort bool) error {
	if videoID == "" {
		return fmt.Errorf("%w: video_id is required", domain.ErrInvalid)
	}
	return c.repo.SetShort(ctx, videoID, isShort)
}

// ListUncheckedShorts returns videos nobody has asked YouTube about yet.
//
// A zero limit means the server's own bound rather than "all of them", the same
// rule the metadata backfill follows: each answer is an HTTP request to
// YouTube, and an unbounded pass is how a caller accidentally asks for
// thousands in a row.
func (c *Catalog) ListUncheckedShorts(ctx context.Context, limit int32) ([]string, error) {
	if limit <= 0 || limit > maxShortCheckBatch {
		limit = maxShortCheckBatch
	}
	return c.repo.ListUncheckedShorts(ctx, limit)
}

func (c *Catalog) FindBySourceURL(ctx context.Context, sourceURL, userID string) (domain.Video, error) {
	if sourceURL == "" {
		return domain.Video{}, fmt.Errorf("%w: source_url is required", domain.ErrInvalid)
	}
	return c.repo.FindBySourceURL(ctx, sourceURL, userID)
}

func (c *Catalog) ListComments(ctx context.Context, videoID string, sort domain.CommentSort, size, offset int32) ([]domain.Comment, int32, error) {
	if videoID == "" {
		return nil, 0, fmt.Errorf("%w: video_id is required", domain.ErrInvalid)
	}
	if sort != domain.SortNewest {
		sort = domain.SortTop
	}
	return c.repo.ListComments(ctx, videoID, sort, clampPage(size, offset))
}

func (c *Catalog) CreateComment(ctx context.Context, videoID, userID, handle, body string, parentID *string) (domain.Comment, error) {
	body = strings.TrimSpace(body)
	switch {
	case videoID == "":
		return domain.Comment{}, fmt.Errorf("%w: video_id is required", domain.ErrInvalid)
	case userID == "":
		return domain.Comment{}, fmt.Errorf("%w: user_id is required", domain.ErrInvalid)
	case body == "":
		return domain.Comment{}, fmt.Errorf("%w: comment body cannot be empty", domain.ErrInvalid)
	}

	return c.repo.CreateComment(ctx, domain.Comment{
		ID:          c.newID(),
		VideoID:     videoID,
		Author:      domain.CommentAuthor{UserID: &userID, Handle: handle},
		Body:        body,
		PublishedAt: c.now(),
	}, parentID)
}

// ImportComments passes YouTube comments through to the repository in one batch.
func (c *Catalog) ImportComments(ctx context.Context, videoID string, comments []domain.ImportComment) (int32, error) {
	if videoID == "" {
		return 0, fmt.Errorf("%w: video_id is required", domain.ErrInvalid)
	}
	return c.repo.ImportComments(ctx, videoID, comments)
}

// RecordWatchProgress is also what refreshes last_accessed_at, so watching a
// video is what protects it from eviction — not merely seeing it in a grid.
func (c *Catalog) RecordWatchProgress(ctx context.Context, userID, videoID string, positionSeconds int32, watchedFraction float32) error {
	if userID == "" || videoID == "" {
		return fmt.Errorf("%w: user_id and video_id are required", domain.ErrInvalid)
	}
	if watchedFraction < 0 {
		watchedFraction = 0
	}
	if watchedFraction > 1 {
		watchedFraction = 1
	}
	if positionSeconds < 0 {
		positionSeconds = 0
	}
	return c.repo.RecordWatchProgress(ctx, userID, videoID, positionSeconds, watchedFraction)
}

func (c *Catalog) SetReaction(ctx context.Context, userID, videoID string, reaction domain.Reaction) (int64, error) {
	if userID == "" || videoID == "" {
		return 0, fmt.Errorf("%w: user_id and video_id are required", domain.ErrInvalid)
	}
	switch reaction {
	case domain.ReactionLike, domain.ReactionDislike, domain.ReactionNone:
	default:
		return 0, fmt.Errorf("%w: unknown reaction %q", domain.ErrInvalid, reaction)
	}
	return c.repo.SetReaction(ctx, userID, videoID, reaction)
}

func (c *Catalog) SetWatchLater(ctx context.Context, userID, videoID string, inWatchLater bool) error {
	if userID == "" || videoID == "" {
		return fmt.Errorf("%w: user_id and video_id are required", domain.ErrInvalid)
	}
	return c.repo.SetWatchLater(ctx, userID, videoID, inWatchLater)
}

func (c *Catalog) ListWatchLater(ctx context.Context, userID string, size, offset int32) ([]domain.Video, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: user_id is required", domain.ErrInvalid)
	}
	return c.repo.ListWatchLater(ctx, userID, clampPage(size, offset))
}

func (c *Catalog) SetSubscription(ctx context.Context, userID, channelID string, subscribed bool) error {
	if userID == "" || channelID == "" {
		return fmt.Errorf("%w: user_id and channel_id are required", domain.ErrInvalid)
	}
	return c.repo.SetSubscription(ctx, userID, channelID, subscribed)
}

func (c *Catalog) ListSubscriptions(ctx context.Context, userID string) ([]domain.Channel, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: user_id is required", domain.ErrInvalid)
	}
	return c.repo.ListSubscriptions(ctx, userID)
}

func (c *Catalog) ListAllSubscribedChannels(ctx context.Context) ([]domain.Channel, error) {
	return c.repo.ListAllSubscribedChannels(ctx)
}

func (c *Catalog) ListHistory(ctx context.Context, userID string, size, offset int32) ([]domain.Video, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: user_id is required", domain.ErrInvalid)
	}
	return c.repo.ListHistory(ctx, userID, clampPage(size, offset))
}

func (c *Catalog) GetStorageUsage(ctx context.Context) (domain.StorageUsage, error) {
	return c.repo.GetStorageUsage(ctx, c.budgetBytes)
}

func (c *Catalog) SetPinned(ctx context.Context, userID, videoID string, pinned bool) error {
	if videoID == "" {
		return fmt.Errorf("%w: video_id is required", domain.ErrInvalid)
	}
	// Required rather than defaulted. Saving is per member now, and a save with
	// no member is a row on a shelf nobody can see and nobody can take back —
	// while still pinning the file against eviction for good.
	if userID == "" {
		return fmt.Errorf("%w: user_id is required", domain.ErrInvalid)
	}
	return c.repo.SetPinned(ctx, userID, videoID, pinned)
}

func (c *Catalog) ListPinnedVideos(ctx context.Context, userID string, size, offset int32) ([]domain.Video, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: user_id is required", domain.ErrInvalid)
	}
	return c.repo.ListPinnedVideos(ctx, userID, clampPage(size, offset))
}

// ---------------------------------------------------------------------------
// Playlists
// ---------------------------------------------------------------------------

func (c *Catalog) ListPlaylists(ctx context.Context, userID string) ([]domain.Playlist, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: user_id is required", domain.ErrInvalid)
	}
	return c.repo.ListPlaylists(ctx, userID)
}

func (c *Catalog) GetPlaylist(ctx context.Context, playlistID, userID string, size, offset int32) (domain.Playlist, []domain.Video, error) {
	if playlistID == "" || userID == "" {
		return domain.Playlist{}, nil, fmt.Errorf("%w: playlist_id and user_id are required", domain.ErrInvalid)
	}
	p, err := c.repo.GetPlaylist(ctx, playlistID, userID)
	if err != nil {
		return domain.Playlist{}, nil, err
	}
	vs, err := c.repo.ListPlaylistVideos(ctx, playlistID, userID, clampPage(size, offset))
	return p, vs, err
}

func (c *Catalog) CreatePlaylist(ctx context.Context, userID, title, description, sourceURL string) (domain.Playlist, error) {
	title = strings.TrimSpace(title)
	if userID == "" {
		return domain.Playlist{}, fmt.Errorf("%w: user_id is required", domain.ErrInvalid)
	}
	// A list with no name cannot be told from another on the playlists page, and
	// there is nowhere else its identity could come from.
	if title == "" {
		return domain.Playlist{}, fmt.Errorf("%w: title is required", domain.ErrInvalid)
	}
	return c.repo.CreatePlaylist(ctx, domain.Playlist{
		ID:          c.newID(),
		UserID:      userID,
		Title:       title,
		Description: strings.TrimSpace(description),
		SourceURL:   sourceURL,
	})
}

func (c *Catalog) UpdatePlaylist(ctx context.Context, playlistID, userID, title, description string) (domain.Playlist, error) {
	if playlistID == "" || userID == "" {
		return domain.Playlist{}, fmt.Errorf("%w: playlist_id and user_id are required", domain.ErrInvalid)
	}
	return c.repo.UpdatePlaylist(ctx, domain.Playlist{
		ID:          playlistID,
		UserID:      userID,
		Title:       strings.TrimSpace(title),
		Description: strings.TrimSpace(description),
	})
}

func (c *Catalog) DeletePlaylist(ctx context.Context, playlistID, userID string) error {
	if playlistID == "" || userID == "" {
		return fmt.Errorf("%w: playlist_id and user_id are required", domain.ErrInvalid)
	}
	return c.repo.DeletePlaylist(ctx, playlistID, userID)
}

func (c *Catalog) SetPlaylistItem(ctx context.Context, playlistID, userID, videoID string, included bool) error {
	if playlistID == "" || userID == "" || videoID == "" {
		return fmt.Errorf("%w: playlist_id, user_id and video_id are required", domain.ErrInvalid)
	}
	return c.repo.SetPlaylistItem(ctx, playlistID, userID, videoID, included)
}

func (c *Catalog) ImportPlaylistItems(ctx context.Context, playlistID, userID string, videoIDs []string, complete bool) (int32, error) {
	if playlistID == "" || userID == "" {
		return 0, fmt.Errorf("%w: playlist_id and user_id are required", domain.ErrInvalid)
	}
	if len(videoIDs) == 0 {
		return 0, nil
	}
	return c.repo.ImportPlaylistItems(ctx, playlistID, userID, videoIDs, complete)
}

func (c *Catalog) ImportWatchLater(ctx context.Context, userID string, videoIDs []string, complete bool) error {
	if userID == "" {
		return fmt.Errorf("%w: user_id is required", domain.ErrInvalid)
	}
	if len(videoIDs) == 0 {
		return nil
	}
	return c.repo.ImportWatchLater(ctx, userID, videoIDs, complete)
}

// ListStalePlaylists is bounded for the same reason the metadata backfill is:
// each answer costs a request to YouTube, on the one session that carries a
// name. A zero limit means the bound rather than "all of them".
func (c *Catalog) ListStalePlaylists(ctx context.Context, limit int32) ([]domain.StalePlaylist, error) {
	if limit <= 0 || limit > maxStalePlaylists {
		limit = maxStalePlaylists
	}
	return c.repo.ListStalePlaylists(ctx, limit)
}
