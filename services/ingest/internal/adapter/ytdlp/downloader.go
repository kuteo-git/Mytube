// Package ytdlp implements domain.Downloader on top of the yt-dlp binary.
//
// go-ytdlp gives typed, generated bindings over the CLI and manages the
// yt-dlp/ffmpeg binaries itself, which is why this service is written in Go
// rather than Python despite yt-dlp being a Python project.
package ytdlp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	// Flat listings omit the single "thumbnail" field and only carry the
	// thumbnails array, ordered smallest first. Fall back to the canonical
	// still URL, which is derivable from the id and always exists.
	if v.ThumbnailURL == "" && len(info.Thumbnails) > 0 {
		v.ThumbnailURL = info.Thumbnails[len(info.Thumbnails)-1].URL
	}
	if v.ThumbnailURL == "" && info.ID != "" {
		v.ThumbnailURL = "https://i.ytimg.com/vi/" + info.ID + "/hqdefault.jpg"
	}

	// yt-dlp returns free-form tags; keep only the hashtag-looking ones so the
	// UI does not fill up with noise.
	for _, tag := range info.Tags {
		if strings.HasPrefix(tag, "#") {
			v.Hashtags = append(v.Hashtags, tag)
		}
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

func (d *Downloader) ListPlaylist(ctx context.Context, url string, limit int32) (string, []domain.ExternalVideo, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	result, err := ytdlp.New().
		FlatPlaylist().
		DumpJSON().
		PlaylistItems(fmt.Sprintf("1:%d", limit)).
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
	return d.fetchSubtitles(ctx, videoURL, dir, videoID, target)
}

// Download fetches a local copy. It asks for a muxed mp4 so the result is
// directly seekable over HTTP range requests without a remux step, and moves
// the moov atom to the front so playback can start before the file is complete.
func (d *Downloader) Download(ctx context.Context, videoURL, videoID string, height int32, onProgress func(domain.Progress)) (domain.DownloadResult, error) {
	dir, target := d.mediaPaths(videoID, height)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return domain.DownloadResult{}, err
	}

	cmd := ytdlp.New().
		Format(fmt.Sprintf("bestvideo[height<=%d]+bestaudio/best[height<=%d]", height, height)).
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
	}, nil
}

// fetchSubtitles runs two passes so the result can say truthfully whether a
// track was written by a human or by a machine. yt-dlp gives both the same
// filename, so the only way to tell them apart is to ask for them separately:
// whatever the manual pass produces is authored, and anything the automatic
// pass adds afterwards is not.
//
// It never returns an error. A video without captions is a working video, and
// the media has already downloaded by the time this runs.
func (d *Downloader) fetchSubtitles(ctx context.Context, videoURL, dir, videoID, target string) []domain.SubtitleTrack {
	d.runSubtitlePass(ctx, videoURL, target, false)
	authored := collectSubtitles(dir, videoID, false)

	d.runSubtitlePass(ctx, videoURL, target, true)
	all := collectSubtitles(dir, videoID, true)

	authoredLanguages := make(map[string]struct{}, len(authored))
	for _, t := range authored {
		authoredLanguages[t.Language] = struct{}{}
	}

	for i := range all {
		_, wasAuthored := authoredLanguages[all[i].Language]
		all[i].Generated = !wasAuthored
	}
	return all
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
