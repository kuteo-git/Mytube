// Package ytdlp implements domain.Downloader on top of the yt-dlp binary.
//
// go-ytdlp gives typed, generated bindings over the CLI and manages the
// yt-dlp/ffmpeg binaries itself, which is why this service is written in Go
// rather than Python despite yt-dlp being a Python project.
package ytdlp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lrstanley/go-ytdlp"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// Upstream URLs are signed and time limited. This is deliberately shorter than
// the real expiry so a client re-resolves before playback breaks mid-video.
const streamTTL = 90 * time.Minute

// Languages worth storing. Fetching every caption track a popular video has
// would write dozens of files nobody reads.
const subtitleLanguages = "en,vi"

type Downloader struct {
	mediaRoot string
}

// New prepares the downloader, installing yt-dlp and ffmpeg on first use.
func New(ctx context.Context, mediaRoot string) *Downloader {
	ytdlp.MustInstall(ctx, nil)
	return &Downloader{mediaRoot: mediaRoot}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func toExternal(info *ytdlp.ExtractedInfo) domain.ExternalVideo {
	v := domain.ExternalVideo{
		ID:              info.ID,
		Title:           deref(info.Title),
		ChannelID:       deref(info.ChannelID),
		ChannelName:     deref(info.Channel),
		DurationSeconds: int32(deref(info.Duration)),
		ViewCount:       int64(deref(info.ViewCount)),
		ThumbnailURL:    deref(info.Thumbnail),
		SourceURL:       deref(info.WebpageURL),
		Description:     deref(info.Description),
	}

	// Flat listings carry no per-entry channel at all: yt-dlp reports the
	// owner once, on the playlist, as playlist_uploader. Without this fallback
	// every scanned video would be attributed to whatever the caller guessed.
	if v.ChannelName == "" {
		v.ChannelName = deref(info.Uploader)
	}
	if v.ChannelName == "" {
		v.ChannelName = deref(info.PlaylistUploader)
	}
	if v.ChannelID == "" {
		v.ChannelID = deref(info.UploaderID)
	}
	if v.ChannelID == "" {
		v.ChannelID = deref(info.PlaylistUploaderID)
	}
	if handle := deref(info.UploaderID); handle != "" {
		v.ChannelHandle = handle
	} else {
		v.ChannelHandle = deref(info.PlaylistUploaderID)
	}
	if v.SourceURL == "" && info.ID != "" {
		v.SourceURL = "https://www.youtube.com/watch?v=" + info.ID
	}

	// The widest still on offer, in preference to the single "thumbnail" field.
	//
	// That field is hqdefault — 480×360 — while the array routinely carries
	// maxresdefault at 1920×1080. A card on a three-column grid is around 560
	// points wide and twice that on a retina screen, so the small one arrives
	// visibly soft; it was chosen only because it happened to be the field with
	// the obvious name.
	if best := widestThumbnail(info.Thumbnails); best != "" {
		v.ThumbnailURL = best
	}
	// Flat listings sometimes carry neither. The canonical still is derivable
	// from the id and exists for every video, which is what makes it a
	// fallback rather than a guess.
	if v.ThumbnailURL == "" && info.ID != "" {
		v.ThumbnailURL = "https://i.ytimg.com/vi/" + info.ID + "/hqdefault.jpg"
	}

	// yt-dlp's thumbnail URLs carry sqp and rs query parameters that sometimes
	// cause YouTube to serve a generic grey placeholder instead of the real
	// still. The canonical URL without parameters always works.
	if idx := strings.IndexByte(v.ThumbnailURL, '?'); idx >= 0 {
		v.ThumbnailURL = v.ThumbnailURL[:idx]
	}

	// yt-dlp returns free-form tags; keep only the hashtag-looking ones so the
	// UI does not fill up with noise.
	for _, tag := range info.Tags {
		if strings.HasPrefix(tag, "#") {
			v.Hashtags = append(v.Hashtags, tag)
		}
	}

	// Categories are absent from flat-playlist listings and present only on a
	// full per-video fetch (Preview). A video can carry more than one; the
	// first is YouTube's primary classification and what becomes the topic.
	if len(info.Categories) > 0 {
		v.Category = info.Categories[0]
	}

	if ts := deref(info.Timestamp); ts > 0 {
		v.PublishedAt = time.Unix(int64(ts), 0).UTC()
	} else if raw := deref(info.UploadDate); len(raw) == 8 {
		if parsed, err := time.Parse("20060102", raw); err == nil {
			v.PublishedAt = parsed
		}
	}
	// PublishedAt is deliberately left zero when unknown. Flat listings omit
	// upload dates, and defaulting to now would render as "1 minute ago" on
	// every card — a plausible-looking lie is worse than a blank.
	return v
}

func (d *Downloader) Search(ctx context.Context, query string, limit int32) ([]domain.ExternalVideo, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// A flat playlist search fetches listing metadata only: no formats are
	// resolved, which is what keeps search fast and reduces upstream load.
	result, err := ytdlp.New().
		FlatPlaylist().
		DumpJSON().
		NoWarnings().
		Run(ctx, fmt.Sprintf("ytsearch%d:%s", limit, query))
	if err != nil {
		return nil, fmt.Errorf("search %q: %w", query, err)
	}

	infos, err := result.GetExtractedInfo()
	if err != nil {
		return nil, err
	}

	videos := make([]domain.ExternalVideo, 0, len(infos))
	for _, info := range infos {
		if info.ID == "" {
			continue
		}
		videos = append(videos, toExternal(info))
	}
	return videos, nil
}

func (d *Downloader) Preview(ctx context.Context, url string) (domain.ExternalVideo, error) {
	result, err := ytdlp.New().
		SkipDownload().
		NoPlaylist().
		DumpJSON().
		NoWarnings().
		Run(ctx, url)
	if err != nil {
		return domain.ExternalVideo{}, fmt.Errorf("preview %q: %w", url, err)
	}

	infos, err := result.GetExtractedInfo()
	if err != nil {
		return domain.ExternalVideo{}, err
	}
	if len(infos) == 0 {
		return domain.ExternalVideo{}, domain.ErrNotFound
	}
	return toExternal(infos[0]), nil
}

// offset skips entries already scanned, which is how the library is deepened
// past the most recent few dozen uploads. yt-dlp's playlist range is 1-based
// and inclusive at both ends.
func (d *Downloader) ListPlaylist(ctx context.Context, url string, offset, limit int32) (string, []domain.ExternalVideo, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	start := offset + 1
	end := offset + limit

	result, err := ytdlp.New().
		FlatPlaylist().
		DumpJSON().
		PlaylistItems(fmt.Sprintf("%d:%d", start, end)).
		NoWarnings().
		Run(ctx, url)
	if err != nil {
		return "", nil, fmt.Errorf("playlist %q: %w", url, err)
	}

	infos, err := result.GetExtractedInfo()
	if err != nil {
		return "", nil, err
	}

	var (
		title  string
		videos []domain.ExternalVideo
	)
	for _, info := range infos {
		if len(info.Entries) > 0 {
			title = deref(info.Title)
			for _, entry := range info.Entries {
				videos = append(videos, toExternal(entry))
			}
			continue
		}
		if info.ID != "" {
			videos = append(videos, toExternal(info))
		}
	}
	return title, videos, nil
}

// ResolveStream picks a progressive format: one file carrying both video and
// audio. Adaptive streams are excluded on purpose — a bare <video> element
// cannot play separate tracks, and in practice this caps instant playback at
// whatever muxed rendition upstream still publishes.
func (d *Downloader) ResolveStream(ctx context.Context, videoURL string) (domain.StreamLocation, error) {
	result, err := ytdlp.New().
		SkipDownload().
		NoPlaylist().
		DumpJSON().
		NoWarnings().
		Run(ctx, videoURL)
	if err != nil {
		return domain.StreamLocation{}, fmt.Errorf("resolve %q: %w", videoURL, err)
	}

	infos, err := result.GetExtractedInfo()
	if err != nil {
		return domain.StreamLocation{}, err
	}
	if len(infos) == 0 {
		return domain.StreamLocation{}, domain.ErrNotFound
	}

	var best *ytdlp.ExtractedFormat
	for _, format := range infos[0].Formats {
		if format.URL == "" {
			continue
		}
		vcodec, acodec := deref(format.VCodec), deref(format.ACodec)
		if vcodec == "" || vcodec == "none" || acodec == "" || acodec == "none" {
			continue // adaptive: video-only or audio-only
		}
		if protocol := deref(format.Protocol); protocol != "https" && protocol != "http" {
			continue // m3u8 and dash need a media-source pipeline
		}
		if extension := deref(format.Extension); extension != "mp4" {
			continue // widest browser support
		}
		if best == nil || deref(format.Height) > deref(best.Height) {
			best = format
		}
	}

	if best == nil {
		return domain.StreamLocation{}, domain.ErrNoProgressiveFormat
	}

	return domain.StreamLocation{
		URL:       best.URL,
		Height:    int32(deref(best.Height)),
		MimeType:  "video/mp4",
		ExpiresAt: time.Now().Add(streamTTL),
	}, nil
}

// widestThumbnail picks the largest still, preferring JPEG at equal width.
//
// The preference is not aesthetic: this is meant to end up on a television
// browser, and WebP is the format those are least likely to decode — the same
// reasoning that picks h264 over AV1 for the video itself.
//
// Entries whose dimensions yt-dlp did not report are counted as zero rather
// than skipped, so a list of nothing but unmeasured entries still yields one.
func widestThumbnail(thumbnails []*ytdlp.ExtractedThumbnail) string {
	var (
		best      string
		bestWidth int
		bestIsJPG bool
	)
	for _, t := range thumbnails {
		if t == nil || t.URL == "" {
			continue
		}
		width := deref(t.Width)
		isJPG := !strings.Contains(t.URL, ".webp")

		switch {
		case best == "":
		case width > bestWidth:
		case width == bestWidth && isJPG && !bestIsJPG:
		default:
			continue
		}
		best, bestWidth, bestIsJPG = t.URL, width, isJPG
	}
	return best
}

// mediaPaths derives the per-video directory and the media file path. Both the
// media transfer and the subtitle pass need them, and the subtitle filenames
// are derived from the media target, so the two must agree exactly.
func (d *Downloader) mediaPaths(videoID string, height int32) (dir, target string) {
	if height <= 0 {
		height = 1080
	}
	dir = filepath.Join(d.mediaRoot, videoID)
	target = filepath.Join(dir, fmt.Sprintf("%dp.mp4", height))
	return dir, target
}

// FetchSubtitles writes the caption files and reports what landed. It runs
// before the media transfer: captions are tiny and the viewer is watching a
// lower-quality upstream stream in the meantime, which is exactly when they are
// most wanted.
func (d *Downloader) FetchSubtitles(ctx context.Context, videoURL, videoID string, height int32) []domain.SubtitleTrack {
	dir, target := d.mediaPaths(videoID, height)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	// Already on disk: nothing to fetch and nothing to publish. Two callers now
	// reach this — the download worker and the request that starts playback —
	// and whichever arrives second must not spend a second round of requests on
	// captions that are already there. Returning nothing rather than the
	// existing tracks is deliberate: the caller publishes what it is given, and
	// the tracks it would be given here have already been published by the
	// caller that fetched them.
	if len(collectSubtitles(dir, videoID, true)) > 0 {
		return nil
	}
	return d.fetchSubtitles(ctx, videoURL, dir, videoID, target)
}

// downloadFormat mirrors remuxFormat in remux.go: h264 first, then anything at
// the height, then any muxed file at all. The two are deliberately the same
// shape — they decide what plays on the same television, and letting them drift
// apart means the downloaded copy can be unplayable where the stream was fine.
const downloadFormat = "bestvideo[height<=%d][vcodec^=avc1]+bestaudio[ext=m4a]/" +
	"bestvideo[height<=%d]+bestaudio/best[height<=%d]"

// Download fetches a local copy. It asks for a muxed mp4 so the result is
// directly seekable over HTTP range requests without a remux step, and moves
// the moov atom to the front so playback can start before the file is complete.
func (d *Downloader) Download(ctx context.Context, videoURL, videoID string, height int32, onProgress func(domain.Progress)) (domain.DownloadResult, error) {
	dir, target := d.mediaPaths(videoID, height)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return domain.DownloadResult{}, err
	}

	cmd := ytdlp.New().
		// Same codec preference as the live remux, and for the same reason.
		//
		// Without it yt-dlp takes whatever is "best", which on YouTube today
		// means AV1 — measured on this library, 32 of 34 downloaded files were
		// AV1 and the remaining two VP9, with not a single h264 among them. AV1
		// is smaller and it decodes fine in a current desktop browser, but the
		// stated destination for this system is a television browser, and that
		// is exactly where it is least likely to be supported. The remux path
		// already asked for h264; the download quietly did not, so the copy that
		// replaces the stream could be the one that will not play.
		//
		// The fallbacks matter as much as the preference: a video published
		// without h264 still downloads, just not in the preferred codec.
		Format(fmt.Sprintf(downloadFormat, height, height, height)).
		MergeOutputFormat("mp4").
		PostProcessorArgs("ffmpeg:-movflags +faststart").
		NoPlaylist().
		NoWarnings().
		NoPart().
		Output(target)

	if onProgress != nil {
		cmd = cmd.ProgressFunc(time.Second, func(update ytdlp.ProgressUpdate) {
			var fraction float32
			if update.TotalBytes > 0 {
				fraction = float32(update.DownloadedBytes) / float32(update.TotalBytes)
			}
			onProgress(domain.Progress{
				Fraction:        fraction,
				DownloadedBytes: int64(update.DownloadedBytes),
				TotalBytes:      int64(update.TotalBytes),
			})
		})
	}

	if _, err := cmd.Run(ctx, videoURL); err != nil {
		return domain.DownloadResult{}, fmt.Errorf("download %q: %w", videoURL, err)
	}

	info, err := os.Stat(target)
	if err != nil {
		return domain.DownloadResult{}, fmt.Errorf("downloaded file missing: %w", err)
	}

	// Captions are fetched by FetchSubtitles, in a separate pass run before this
	// one. Asking for them in the same command means a caption failure — a 429
	// is common — aborts the whole download, losing a video that was otherwise
	// fine. Optional data must not be able to break required data.
	return domain.DownloadResult{
		MediaPath: filepath.Join(videoID, filepath.Base(target)),
		SizeBytes: info.Size(),
		Subtitles: collectSubtitles(dir, videoID, true),
	}, nil
}

// fetchSubtitles runs two passes so the result can say truthfully whether a
// track was written by a human or by a machine. yt-dlp gives both the same
// filename, so the only way to tell them apart is to ask for them separately.
//
// The two passes run at the same time, into a directory each.
//
// They used to run one after the other, and told authored from automatic by
// *order*: whatever existed after the first pass was written by a human. That
// made the second pass wait on the first for no reason other than the naming
// collision — and captions are wanted precisely during the early seconds when
// the viewer is watching the lower-quality upstream stream, so the wait was
// spent exactly where it hurt. Writing each pass somewhere of its own answers
// the same question by *place* instead, and neither has to wait.
//
// It never returns an error. A video without captions is a working video.
func (d *Downloader) fetchSubtitles(ctx context.Context, videoURL, dir, videoID, target string) []domain.SubtitleTrack {
	base := filepath.Base(target)
	authoredDir := filepath.Join(dir, ".subs-authored")
	autoDir := filepath.Join(dir, ".subs-auto")

	// Leftovers from an interrupted run would be read as this run's results.
	_ = os.RemoveAll(authoredDir)
	_ = os.RemoveAll(autoDir)
	if err := os.MkdirAll(authoredDir, 0o755); err != nil {
		return nil
	}
	if err := os.MkdirAll(autoDir, 0o755); err != nil {
		return nil
	}
	defer func() {
		_ = os.RemoveAll(authoredDir)
		_ = os.RemoveAll(autoDir)
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		d.runSubtitlePass(ctx, videoURL, filepath.Join(authoredDir, base), false)
	}()
	go func() {
		defer wg.Done()
		d.runSubtitlePass(ctx, videoURL, filepath.Join(autoDir, base), true)
	}()
	wg.Wait()

	return mergeSubtitlePasses(authoredDir, autoDir, dir, videoID)
}

// mergeSubtitlePasses moves both passes' files into the video's directory,
// authored first so a language a human captioned is never replaced by the
// machine's version of the same language.
func mergeSubtitlePasses(authoredDir, autoDir, dir, videoID string) []domain.SubtitleTrack {
	var tracks []domain.SubtitleTrack
	taken := map[string]struct{}{}

	for _, pass := range []struct {
		dir       string
		generated bool
	}{{authoredDir, false}, {autoDir, true}} {
		for _, track := range collectSubtitles(pass.dir, videoID, true) {
			if _, already := taken[track.Language]; already {
				continue
			}
			name := filepath.Base(track.Path)
			if err := os.Rename(filepath.Join(pass.dir, name), filepath.Join(dir, name)); err != nil {
				continue
			}
			taken[track.Language] = struct{}{}
			track.Generated = pass.generated
			track.Path = filepath.Join(videoID, name)
			tracks = append(tracks, track)
		}
	}
	return tracks
}

func (d *Downloader) runSubtitlePass(ctx context.Context, videoURL, target string, automatic bool) {
	// YouTube rate-limits the caption endpoint far more aggressively than the
	// media one and answers 429 readily. Pacing requests and retrying is what
	// makes captions arrive at all; failing costs nothing.
	cmd := ytdlp.New().
		SkipDownload().
		SubLangs(subtitleLanguages).
		ConvertSubs("vtt").
		SleepRequests(1).
		Retries("3").
		NoPlaylist().
		NoWarnings().
		Output(target)

	if automatic {
		cmd = cmd.WriteAutoSubs()
	} else {
		cmd = cmd.WriteSubs()
	}

	_, _ = cmd.Run(ctx, videoURL)
}

// collectSubtitles reads back what yt-dlp wrote. The filenames follow
// "<base>.<lang>.vtt", and there is no reliable way to know in advance which
// languages exist, so the directory is the source of truth.
func collectSubtitles(dir, videoID string, _ bool) []domain.SubtitleTrack {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var tracks []domain.SubtitleTrack
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".vtt") {
			continue
		}

		// "720p.en.vtt" -> "en"; "720p.en-orig.vtt" -> "en-orig".
		parts := strings.Split(strings.TrimSuffix(name, ".vtt"), ".")
		if len(parts) < 2 {
			continue
		}
		language := parts[len(parts)-1]

		tracks = append(tracks, domain.SubtitleTrack{
			Language: language,
			Label:    subtitleLabel(language),
			Path:     filepath.Join(videoID, name),
			// Set by the caller, which knows which pass produced the file.
		})
	}
	return tracks
}

func subtitleLabel(language string) string {
	switch strings.ToLower(strings.SplitN(language, "-", 2)[0]) {
	case "en":
		return "English"
	case "vi":
		return "Tiếng Việt"
	default:
		return strings.ToUpper(language)
	}
}

// ChannelInfo reads a channel's own metadata by asking yt-dlp for the channel
// URL with no entries at all. `--playlist-items 0` is the cheap way to do it:
// it returns the container's metadata and skips every video in it.
func (d *Downloader) ChannelInfo(ctx context.Context, channelURL string) (domain.ChannelMetadata, error) {
	result, err := ytdlp.New().
		DumpSingleJSON().
		FlatPlaylist().
		PlaylistItems("0").
		NoWarnings().
		Run(ctx, channelURL)
	if err != nil {
		return domain.ChannelMetadata{}, fmt.Errorf("channel info %q: %w", channelURL, err)
	}

	var payload struct {
		ID         string `json:"channel_id"`
		Uploader   string `json:"uploader"`
		Channel    string `json:"channel"`
		UploaderID string `json:"uploader_id"`
		Followers  int64  `json:"channel_follower_count"`
		Thumbnails []struct {
			URL string `json:"url"`
			ID  string `json:"id"`
		} `json:"thumbnails"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		return domain.ChannelMetadata{}, fmt.Errorf("channel info %q: %w", channelURL, err)
	}

	name := payload.Channel
	if name == "" {
		name = payload.Uploader
	}

	meta := domain.ChannelMetadata{
		ID:              payload.ID,
		Name:            name,
		Handle:          payload.UploaderID,
		SubscriberCount: payload.Followers,
	}

	// yt-dlp labels channel artwork by aspect: "avatar_uncropped" is the round
	// picture, "banner_uncropped" the wide header. Falling back to the widest
	// image for the banner is safe; falling back for the avatar is not, because
	// a wide image in a round frame looks broken.
	for _, t := range payload.Thumbnails {
		switch {
		case strings.HasPrefix(t.ID, "avatar"):
			meta.AvatarURL = t.URL
		case strings.HasPrefix(t.ID, "banner"):
			meta.BannerURL = t.URL
		}
	}
	return meta, nil
}

// saveChannelImage downloads artwork into the media root and returns its path
// relative to that root, or "" if there was nothing to fetch. Artwork is
// optional decoration: a failure here must never fail a scan.
func (d *Downloader) saveChannelImage(ctx context.Context, url, channelID, kind string) string {
	if url == "" {
		return ""
	}

	dir := filepath.Join(d.mediaRoot, "channels", channelID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	name := kind + ".jpg"
	file, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()

	if _, err := io.Copy(file, resp.Body); err != nil {
		return ""
	}
	return filepath.Join("channels", channelID, name)
}

// saveThumbnail downloads the best available thumbnail for a video.
// Tries maxresdefault (1920×1080), then sddefault (640×480), then the
// URL passed in (usually hqdefault at 480×360). Cards are ~560 px wide
// and twice that on retina — the larger stills are the difference between
// sharp and soft.
func (d *Downloader) saveThumbnail(ctx context.Context, url, videoID string) string {
	if videoID == "" {
		return ""
	}

	dir := filepath.Join(d.mediaRoot, "thumbnails")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}

	candidates := []string{
		"https://i.ytimg.com/vi/" + videoID + "/maxresdefault.jpg",
		"https://i.ytimg.com/vi/" + videoID + "/sddefault.jpg",
	}
	if url != "" {
		candidates = append(candidates, url)
	}

	for _, u := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
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

		dst := filepath.Join(dir, videoID+".jpg")
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
		return filepath.Join("thumbnails", videoID+".jpg")
	}
	return ""
}

// SaveThumbnail is the domain adapter.
func (d *Downloader) SaveThumbnail(ctx context.Context, url, videoID string) string {
	return d.saveThumbnail(ctx, url, videoID)
}

// FetchChannelArtwork downloads the avatar and banner and returns their paths
// under the media root.
func (d *Downloader) FetchChannelArtwork(ctx context.Context, m domain.ChannelMetadata) (avatarPath, bannerPath string) {
	return d.saveChannelImage(ctx, m.AvatarURL, m.ID, "avatar"),
		d.saveChannelImage(ctx, m.BannerURL, m.ID, "banner")
}
