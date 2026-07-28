package usecase

import (
	"context"
	"fmt"
	"log/slog"
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

	for _, topic := range config.Topics {
		for _, source := range topic.Sources {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}

			seen, added, err := s.scanSource(ctx, topic.Name, source, config.PerSourceLimit)
			result.SourcesScanned++
			result.VideosSeen += seen
			result.VideosAdded += added

			if err != nil {
				// One broken source must not stop the rest: a deleted playlist
				// should cost that topic its videos, not the whole scan.
				result.SourcesFailed++
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", source, err))
				s.logger.Warn("scan source", "topic", topic.Name, "source", source, "error", err)
			}
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

		// The topic comes from the source it was found in, never from YouTube's
		// own categories. Catalog merges topics on conflict, so a video in two
		// topics accumulates both rather than losing one.
		v.Topics = []string{topicName}

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
