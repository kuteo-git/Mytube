package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// Scanner keeps the catalog in step with topics.yaml.
//
// It fetches listing metadata only. Nothing here downloads media: a video
// becomes a row that the feed can rank, and bytes are fetched later, if and
// when someone presses play.
type Scanner struct {
	topics   domain.TopicSource
	fetch    domain.Downloader
	library  domain.Library
	logger   *slog.Logger
	interval time.Duration

	// A scan takes minutes and hits an external service; running two at once
	// would double the request rate for no benefit.
	mu       sync.Mutex
	running  bool
	lastScan domain.ScanResult
}

func NewScanner(topics domain.TopicSource, fetch domain.Downloader, library domain.Library, logger *slog.Logger, interval time.Duration) *Scanner {
	return &Scanner{
		topics:   topics,
		fetch:    fetch,
		library:  library,
		logger:   logger,
		interval: interval,
	}
}

// Run scans once at startup, then on the configured interval. Twelve hours is
// the intended cadence: new uploads do not appear by the minute, and a low rate
// keeps this from looking like a bot to the source.
func (s *Scanner) Run(ctx context.Context) {
	if _, err := s.ScanNow(ctx); err != nil {
		s.logger.Error("initial scan", "error", err)
	}

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.ScanNow(ctx); err != nil {
				s.logger.Error("scheduled scan", "error", err)
			}
		}
	}
}

func (s *Scanner) LastScan() domain.ScanResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastScan
}

// ScanNow backs both the timer and the manual Refresh button.
func (s *Scanner) ScanNow(ctx context.Context) (domain.ScanResult, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return domain.ScanResult{}, fmt.Errorf("%w: a scan is already running", domain.ErrInvalid)
	}
	s.running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	result := domain.ScanResult{StartedAt: time.Now()}

	config, err := s.topics.Load(ctx)
	if err != nil {
		return result, err
	}

	targets := s.sources(ctx, config)

	// Channel artwork is fetched once per source per scan, ahead of the video
	// listing: it is decoration, and a failure here must never cost a source
	// its videos.
	for _, target := range targets {
		if meta, err := s.fetch.ChannelInfo(ctx, target.URL); err != nil {
			s.logger.Warn("channel info", "source", target.URL, "error", err)
		} else if meta.ID != "" {
			avatar, banner := s.fetch.FetchChannelArtwork(ctx, meta)
			if err := s.library.UpsertChannelArtwork(ctx, meta, avatar, banner); err != nil {
				s.logger.Warn("store channel artwork", "channel", meta.ID, "error", err)
			}
		}
	}

	for _, target := range targets {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		seen, added, err := s.scanSource(ctx, target.TopicName, target.URL, config.PerSourceLimit)
		result.SourcesScanned++
		result.VideosSeen += seen
		result.VideosAdded += added

		if err != nil {
			// One broken source must not stop the rest: a deleted playlist
			// should cost that topic its videos, not the whole scan.
			result.SourcesFailed++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", target.URL, err))
			s.logger.Warn("scan source", "topic", target.TopicName, "source", target.URL, "error", err)
		}
	}

	result.Duration = time.Since(result.StartedAt)

	s.mu.Lock()
	s.lastScan = result
	s.mu.Unlock()

	s.logger.Info("scan complete",
		"sources", result.SourcesScanned,
		"failed", result.SourcesFailed,
		"seen", result.VideosSeen,
		"added", result.VideosAdded,
		"took", result.Duration.Round(time.Second))
	return result, nil
}

func (s *Scanner) scanSource(ctx context.Context, topicName, source string, limit int32) (seen, added int, err error) {
	_, videos, err := s.fetch.ListPlaylist(ctx, source, 0, limit)
	if err != nil {
		return 0, 0, err
	}

	for _, v := range videos {
		if v.ID == "" || v.SourceURL == "" {
			continue
		}
		seen++

		s.assignTopic(ctx, &v, topicName)

		// Flat listings omit the channel for some sources; without one the
		// catalog row cannot be written, so fall back to the source itself.
		if v.ChannelID == "" {
			v.ChannelID = "src:" + source
			if v.ChannelName == "" {
				v.ChannelName = topicName
			}
		}

		if err := s.library.UpsertChannel(ctx, v); err != nil {
			s.logger.Warn("upsert channel", "channel", v.ChannelID, "error", err)
			continue
		}

		// QUEUED means "known, not on disk". The feed can rank it; pressing
		// play is what turns it into a download.
		if err := s.library.UpsertVideo(ctx, v, "QUEUED"); err != nil {
			s.logger.Warn("upsert video", "video", v.ID, "error", err)
			continue
		}
		added++
	}

	return seen, added, nil
}

// assignTopic decides what topic a video is filed under.
//
// YouTube's own category is the source of truth now (CLAUDE.md §7): a video
// gets whatever category YouTube assigns it, the same taxonomy YouTube's own
// subscriptions/explore chips use. That information only exists on a full
// per-video fetch, which costs roughly two seconds — acceptable for a video
// nobody has seen before, ruinous if paid on every video on every scan. So
// this only fetches for videos the library does not already know: an
// already-known video keeps its first-assigned topic and this function does
// not touch the network for it at all.
//
// If the fetch fails, or the video carries no category, curated sources fall
// back to the topics.yaml name so they are never topic-less; subscriptions
// (topicName == "") have no such fallback and simply stay unfiled, same as
// before this change.
func (s *Scanner) assignTopic(ctx context.Context, v *domain.ExternalVideo, topicName string) {
	if _, known, err := s.library.FindBySourceURL(ctx, v.SourceURL); err != nil {
		s.logger.Warn("check known video", "video", v.ID, "error", err)
	} else if known {
		return
	}

	full, err := s.fetch.Preview(ctx, v.SourceURL)
	if err != nil || full.Category == "" {
		if err != nil {
			s.logger.Warn("fetch category", "video", v.ID, "error", err)
		}
		if topicName != "" {
			v.Topics = []string{topicName}
		}
		return
	}
	v.Topics = []string{full.Category}
}

// scanTarget is one thing to scan. An empty TopicName marks a subscription:
// videos from it join the library without being filed under a topic, because
// nobody said the channel belongs to one.
type scanTarget struct {
	TopicName string
	URL       string
}

// sources merges the two content sources this system has.
//
// topics.yaml is curated ahead of time and lives in git. Subscriptions are
// chosen while using the app and live in the database. Merging them here — at
// the point of scanning, rather than by writing subscriptions back into the
// file — keeps the file something the owner edits and the app never touches.
func (s *Scanner) sources(ctx context.Context, config domain.TopicConfig) []scanTarget {
	var targets []scanTarget
	for _, topic := range config.Topics {
		for _, url := range topic.Sources {
			targets = append(targets, scanTarget{TopicName: topic.Name, URL: url})
		}
	}

	channels, err := s.library.ListSubscribedChannels(ctx)
	if err != nil {
		// A subscription list that cannot be read must not stop the curated
		// sources from being scanned.
		s.logger.Warn("list subscribed channels", "error", err)
		return targets
	}

	for _, c := range channels {
		targets = append(targets, scanTarget{URL: channelVideosURL(c)})
	}
	return targets
}

// channelVideosURL prefers the handle, which is stable and readable. Some
// channels have none — Tinh tế is the known case in topics.yaml — and those
// need the channel id form instead.
func channelVideosURL(c domain.SubscribedChannel) string {
	if c.Handle != "" {
		handle := c.Handle
		if !strings.HasPrefix(handle, "@") {
			handle = "@" + handle
		}
		return "https://www.youtube.com/" + handle + "/videos"
	}
	return "https://www.youtube.com/channel/" + c.ID + "/videos"
}
