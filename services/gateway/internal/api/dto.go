// Package api is the browser-facing edge of the system.
//
// It is the only place that speaks REST/JSON. Everything behind it speaks
// ConnectRPC, and the browser never learns that more than one service exists —
// which is also what keeps CORS and TLS to a single origin, the thing that
// makes a self-signed certificate workable on a TV.
package api

import (
	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
)

// The wire shape below is hand-written rather than protojson-encoded on
// purpose: the client should not have to care about protobuf naming quirks
// (`MEDIA_STATE_READY`, string-encoded int64) just to render a card.

type channelDTO struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Handle          string `json:"handle"`
	AvatarPath      string `json:"avatarPath"`
	SubscriberCount int64  `json:"subscriberCount"`
	Verified        bool   `json:"verified"`
	Subscribed      bool   `json:"subscribed"`
}

type userStateDTO struct {
	WatchProgress        float32 `json:"watchProgress"`
	WatchPositionSeconds int32   `json:"watchPositionSeconds"`
	Reaction             string  `json:"reaction"`
	InWatchLater         bool    `json:"inWatchLater"`
}

type videoDTO struct {
	ID              string        `json:"id"`
	Title           string        `json:"title"`
	Channel         channelDTO    `json:"channel"`
	DurationSeconds int32         `json:"durationSeconds"`
	ViewCount       int64         `json:"viewCount"`
	PublishedAt     string        `json:"publishedAt"`
	AddedAt         string        `json:"addedAt"`
	ThumbnailPath   string        `json:"thumbnailPath"`
	Description     string        `json:"description"`
	Hashtags        []string      `json:"hashtags"`
	Categories      []string      `json:"categories"`
	MediaState      string        `json:"mediaState"`
	MediaPath       string        `json:"mediaPath"`
	SizeBytes       int64         `json:"sizeBytes"`
	Pinned          bool          `json:"pinned"`
	SourceURL       string        `json:"sourceUrl"`
	LikeCount       int64         `json:"likeCount"`
	UserState       *userStateDTO `json:"userState,omitempty"`
	// Why the recommendation service surfaced this video. Empty outside feeds.
	Reason string `json:"reason,omitempty"`
}

type commentDTO struct {
	ID          string       `json:"id"`
	AuthorLabel string       `json:"authorHandle"`
	AvatarPath  string       `json:"avatarPath"`
	Text        string       `json:"text"`
	PublishedAt string       `json:"publishedAt"`
	LikeCount   int64        `json:"likeCount"`
	PinnedBy    string       `json:"pinnedBy,omitempty"`
	Replies     []commentDTO `json:"replies"`
}

type feedResponse struct {
	Videos        []videoDTO `json:"videos"`
	NextPageToken string     `json:"nextPageToken,omitempty"`
}

type commentsResponse struct {
	Comments      []commentDTO `json:"comments"`
	TotalCount    int32        `json:"totalCount"`
	NextPageToken string       `json:"nextPageToken,omitempty"`
}

type categoryDTO struct {
	Name       string `json:"name"`
	VideoCount int32  `json:"videoCount"`
}

type storageResponse struct {
	UsedBytes          int64      `json:"usedBytes"`
	BudgetBytes        int64      `json:"budgetBytes"`
	DiskFreeBytes      int64      `json:"diskFreeBytes"`
	VideoCount         int32      `json:"videoCount"`
	EvictedCount       int32      `json:"evictedCount"`
	EvictionCandidates []videoDTO `json:"evictionCandidates"`
}

// trimEnumPrefix turns MEDIA_STATE_READY into READY so the client can switch on
// a plain word.
func trimEnumPrefix(value, prefix string) string {
	if len(value) > len(prefix) && value[:len(prefix)] == prefix {
		return value[len(prefix):]
	}
	return value
}

func toChannelDTO(c *catalogv1.Channel) channelDTO {
	if c == nil {
		return channelDTO{}
	}
	return channelDTO{
		ID:              c.GetId(),
		Name:            c.GetName(),
		Handle:          c.GetHandle(),
		AvatarPath:      c.GetAvatarPath(),
		SubscriberCount: c.GetSubscriberCount(),
		Verified:        c.GetVerified(),
		Subscribed:      c.GetSubscribed(),
	}
}

func toVideoDTO(v *catalogv1.Video) videoDTO {
	if v == nil {
		return videoDTO{}
	}
	dto := videoDTO{
		ID:              v.GetId(),
		Title:           v.GetTitle(),
		Channel:         toChannelDTO(v.GetChannel()),
		DurationSeconds: v.GetDurationSeconds(),
		ViewCount:       v.GetViewCount(),
		PublishedAt:     v.GetPublishedAt().AsTime().UTC().Format("2006-01-02T15:04:05Z"),
		AddedAt:         v.GetAddedAt().AsTime().UTC().Format("2006-01-02T15:04:05Z"),
		ThumbnailPath:   v.GetThumbnailPath(),
		Description:     v.GetDescription(),
		Hashtags:        orEmpty(v.GetHashtags()),
		Categories:      orEmpty(v.GetCategories()),
		MediaState:      trimEnumPrefix(v.GetMediaState().String(), "MEDIA_STATE_"),
		MediaPath:       v.GetMediaPath(),
		SizeBytes:       v.GetSizeBytes(),
		Pinned:          v.GetPinned(),
		SourceURL:       v.GetSourceUrl(),
		LikeCount:       v.GetLikeCount(),
	}

	if us := v.GetUserState(); us != nil {
		dto.UserState = &userStateDTO{
			WatchProgress:        us.GetWatchProgress(),
			WatchPositionSeconds: us.GetWatchPositionSeconds(),
			Reaction:             trimEnumPrefix(us.GetReaction().String(), "REACTION_"),
			InWatchLater:         us.GetInWatchLater(),
		}
	}
	return dto
}

func toCommentDTO(c *catalogv1.Comment) commentDTO {
	dto := commentDTO{
		ID:          c.GetId(),
		AuthorLabel: c.GetAuthor().GetHandle(),
		AvatarPath:  c.GetAuthor().GetAvatarPath(),
		Text:        c.GetText(),
		PublishedAt: c.GetPublishedAt().AsTime().UTC().Format("2006-01-02T15:04:05Z"),
		LikeCount:   c.GetLikeCount(),
		PinnedBy:    c.GetPinnedBy(),
		Replies:     []commentDTO{},
	}
	for _, reply := range c.GetReplies() {
		dto.Replies = append(dto.Replies, toCommentDTO(reply))
	}
	return dto
}

// orEmpty keeps JSON arrays as [] rather than null, so the client never has to
// guard against a missing list.
func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
