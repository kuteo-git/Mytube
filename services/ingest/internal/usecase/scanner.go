package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// Scanner keeps the catalog in step with topics.yaml.
//
// It fetches listing metadata only. Nothing here downloads media: a video
// becomes a row that the feed can rank, and bytes are fetched later, if and
// when someone presses play.
type Scanner struct {
	topics domain.TopicSource
	fetch  domain.Downloader
	// channels is YouTube's own browse API. Preferred over the flat playlist
	// listing because it is the only source that carries view counts and upload
	// dates; it is undocumented, so every use falls back to fetch.
	channels domain.ChannelSource
	library domain.Library
	// Where passes are kept so the Activity page can show more than the last
	// one. Optional: a scanner without it still scans, and the tests that
	// exercise scanning do not need a database to do it.
	scans    domain.ScanStore
	logger   *slog.Logger
	interval time.Duration

	// A scan takes minutes and hits an external service; running two at once
	// would double the request rate for no benefit.
	mu       sync.Mutex
	running  bool
	lastScan domain.ScanResult
}

// How long a pass stays on the Activity page.
//
// Thirty days, because the question it answers is asked in days — "this channel
// has stopped producing new videos, has the scan been running?" — and a month
// covers every version of that question anybody has brought to this page.
const scanRetention = 30 * 24 * time.Hour

func NewScanner(
	topics domain.TopicSource,
	fetch domain.Downloader,
	channels domain.ChannelSource,
	library domain.Library,
	scans domain.ScanStore,
	logger *slog.Logger,
	interval time.Duration,
) *Scanner {
	return &Scanner{
		topics:   topics,
		fetch:    fetch,
		channels: channels,
		library:  library,
		scans:    scans,
		logger:   logger,
		interval: interval,
	}
}

// Run scans once at startup, then on the configured interval.
//
// The interval is the whole of how fresh the feed can be — nothing uploaded
// upstream can appear here before a pass has seen it. Hourly is the default and
// is affordable because a pass uses flat listings, which are cheap; the
// expensive part is the per-video metadata fetch, which the scanner
// deliberately does not do. Going much below an hour buys little, since uploads
// do not arrive by the minute, and starts to look like a bot to the source.
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

	// Channel metadata is fetched once per source per scan. It carries the
	// artwork, and it is also the only place the channel's real identity is
	// known: the browse listing returns videos without saying whose they are,
	// so without this every scanned video would be attributed to a synthetic
	// channel invented from the source URL.
	owners := make(map[string]domain.ChannelMetadata, len(targets))
	for _, target := range targets {
		meta, err := s.fetch.ChannelInfo(ctx, target.URL)
		if err != nil {
			s.logger.Warn("channel info", "source", target.URL, "error", err)
			continue
		}
		if meta.ID == "" {
			continue
		}

		owners[target.URL] = meta
		avatar, banner := s.fetch.FetchChannelArtwork(ctx, meta)
		if err := s.library.UpsertChannelArtwork(ctx, meta, avatar, banner); err != nil {
			s.logger.Warn("store channel artwork", "channel", meta.ID, "error", err)
		}
	}

	for _, target := range targets {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		seen, added, err := s.scanSource(ctx, target.TopicName, target.URL, owners[target.URL], config.PerSourceLimit)
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

	// Written after the in-memory copy, and a failure here is logged rather
	// than returned: the scan has already done its work, and losing the record
	// of a pass is not the same as the pass not happening.
	if s.scans != nil {
		if err := s.scans.RecordScan(ctx, result, scanRetention); err != nil {
			s.logger.Warn("record scan", "error", err)
		}
	}

	s.logger.Info("scan complete",
		"sources", result.SourcesScanned,
		"failed", result.SourcesFailed,
		"seen", result.VideosSeen,
		"added", result.VideosAdded,
		"took", result.Duration.Round(time.Second))
	return result, nil
}

func (s *Scanner) scanSource(
	ctx context.Context,
	topicName, source string,
	owner domain.ChannelMetadata,
	limit int32,
) (seen, added int, err error) {
	videos, err := s.listSource(ctx, source, limit)
	if err != nil {
		return 0, 0, err
	}

	// RSS feeds give exact publish dates for the 15 most recent uploads, which a
	// flat playlist listing never carries. This is a supplementary pass: a channel
	// with no reachable feed still gets scanned, just without the dates the feed
	// would have provided.
	rssByID := s.fetchChannelFeed(ctx, owner.ID)

	for _, v := range videos {
		if v.ID == "" || v.SourceURL == "" {
			continue
		}
		seen++

		// The feed carries exact dates and view counts where the listing does not.
		// Filling gaps rather than overwriting: a value already present from a
		// previous full fetch (Preview) is more trustworthy than RSS.
		if rss, ok := rssByID[v.ID]; ok {
			if v.PublishedAt.IsZero() && !rss.PublishedAt.IsZero() {
				v.PublishedAt = rss.PublishedAt
			}
			if v.ViewCount == 0 && rss.ViewCount > 0 {
				v.ViewCount = rss.ViewCount
			}
		}

		// The topic comes from the source it was found in. An empty topicName
		// marks a subscription: its videos join the library unfiled, because
		// nobody said the channel belongs to a topic. YouTube's own category is
		// filled in later, free of charge, the first time something actually
		// fetches full metadata for the video — see CLAUDE.md §7.
		if topicName != "" {
			v.Topics = []string{topicName}
		}

		applyOwner(&v, owner, isPlaylistSource(source))
		if v.ChannelID == "" || v.ChannelName == "" {
			// Attributing the video to an invented channel would put a row in
			// the catalog that no channel page can ever show.
			s.logger.Warn("skipping video with no known channel", "video", v.ID, "source", source)
			continue
		}

		if err := s.library.UpsertChannel(ctx, v); err != nil {
			s.logger.Warn("upsert channel", "channel", v.ChannelID, "error", err)
			continue
		}

		// Download the thumbnail locally so the frontend never depends on
		// YouTube's CDN. A failure here is decoration lost: UpsertVideo still
		// gets the remote URL as a fallback.
		if local := s.fetch.SaveThumbnail(ctx, v.ThumbnailURL, v.ID); local != "" {
			v.ThumbnailURL = local
		}

		// Detect language from the title's script. Latin-script titles
		// (English, Vietnamese, etc.) are marked "en" so the language
		// filter accepts them. Non-Latin (Arabic, Thai, CJK) stay empty,
		// which hides them when a filter is active.
		if v.Language == "" && v.Title != "" && isLatinTitle(v.Title) {
			v.Language = "en"
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

// fetchChannelFeed reads a channel's RSS feed and returns its entries keyed by
// video id. Returns nil (not an empty map) when the feed is unreachable or the
// channel id is unknown, so the caller can distinguish "nothing to apply" from
// "the feed was empty".
func (s *Scanner) fetchChannelFeed(ctx context.Context, channelID string) map[string]domain.RSSEntry {
	if channelID == "" {
		return nil
	}

	entries, err := s.fetch.FetchChannelFeed(ctx, channelID)
	if err != nil {
		s.logger.Debug("rss feed", "channel", channelID, "error", err)
		return nil
	}

	out := make(map[string]domain.RSSEntry, len(entries))
	for _, e := range entries {
		out[e.VideoID] = e
	}
	return out
}

// applyOwner fills in — and for a channel source, corrects — who a video
// belongs to.
//
// Neither listing reliably names the owner: a flat listing omits it for some
// sources, and the browse listing never carries it at all. Worse, when the flat
// listing does supply one it is often the handle ("@mkbhd") rather than the
// channel id ("UC..."), and the two forms produce two catalog rows for one
// channel — which is how the library ended up with sixty duplicate channels.
//
// For a channel source every video is that channel's, so the metadata fetched
// once for the source is authoritative and its id wins. A playlist is
// different: it can hold videos from many channels, so there the owner only
// fills gaps and never overrides what the listing said.
func applyOwner(v *domain.ExternalVideo, owner domain.ChannelMetadata, fromPlaylist bool) {
	authoritative := !fromPlaylist && owner.ID != ""

	// The listing often supplies a handle ("@mkbhd") where an id belongs.
	// Treat it as empty so the owner can fill in the real UC... id.
	broken := strings.HasPrefix(v.ChannelID, "@")

	if v.ChannelID == "" || broken || authoritative {
		if owner.ID != "" && !strings.HasPrefix(owner.ID, "@") {
			v.ChannelID = owner.ID
		}
	}
	if v.ChannelName == "" || authoritative {
		if owner.Name != "" {
			v.ChannelName = owner.Name
		}
	}
	if v.ChannelHandle == "" || authoritative {
		if owner.Handle != "" {
			v.ChannelHandle = owner.Handle
		}
	}
}

// isPlaylistSource distinguishes a playlist from a channel. Only a channel has
// a single owner that can be applied to every video it lists.
func isPlaylistSource(source string) bool {
	return strings.Contains(source, "list=")
}

// listSource reads a source's videos, preferring YouTube's browse API.
//
// The flat playlist listing yt-dlp produces carries neither view counts nor
// upload dates, which is why almost every card in the library used to show a
// bare title. Browse carries both. It is an undocumented API, so this falls
// back to the flat listing on any failure — a scan that loses view counts is a
// working scan, a scan that fails is not.
//
// Playlist sources are left to yt-dlp: browse addresses channels, and a
// playlist is not one.
func (s *Scanner) listSource(ctx context.Context, source string, limit int32) ([]domain.ExternalVideo, error) {
	if s.channels != nil && !isPlaylistSource(source) {
		videos, err := s.listViaBrowse(ctx, source, limit)
		if err != nil {
			s.logger.Warn("browse listing, falling back to flat listing",
				"source", source, "error", err)
		} else if len(videos) > 0 {
			return videos, nil
		}
	}

	_, videos, err := s.fetch.ListPlaylist(ctx, source, 0, limit)
	return videos, err
}

// listViaBrowse pages the browse API until it has enough videos, since one
// browse page is thirty and per_source_limit may be larger.
func (s *Scanner) listViaBrowse(ctx context.Context, source string, limit int32) ([]domain.ExternalVideo, error) {
	browseID, err := s.channels.ResolveChannelID(ctx, channelRefFromURL(source))
	if err != nil {
		return nil, err
	}

	var (
		out   []domain.ExternalVideo
		token string
	)
	for int32(len(out)) < limit {
		page, err := s.channels.ChannelUploads(ctx, browseID, token)
		if err != nil {
			// Whatever arrived so far is still usable; the caller decides
			// whether it is enough to skip the fallback.
			return out, err
		}
		if len(page.Videos) == 0 {
			break
		}

		out = append(out, page.Videos...)
		token = page.NextPageToken
		if token == "" {
			break
		}
		// Continuations address the channel on their own, so the id is only
		// needed for the first request.
		browseID = ""
	}

	if int32(len(out)) > limit {
		out = out[:limit]
	}
	return out, nil
}

// channelRefFromURL reduces a source URL to the handle or channel id the browse
// API addresses channels by.
func channelRefFromURL(source string) string {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(source, "/"), "/videos")
	if idx := strings.LastIndex(trimmed, "/channel/"); idx >= 0 {
		return trimmed[idx+len("/channel/"):]
	}
	if idx := strings.LastIndex(trimmed, "/@"); idx >= 0 {
		return trimmed[idx+1:]
	}
	return trimmed
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


// isLatinTitle reports whether every letter in the title belongs to the Latin
// script. Titles mixing Latin with other scripts are treated as non-Latin.
func isLatinTitle(title string) bool {
	for _, r := range title {
		if unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r) {
			return false
		}
	}
	return true
}
