package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
)

// castAudioName is what the sound of a downloaded video is kept as, beside its
// media file. Catalog's eviction removes it by the same name
// (usecase.CastAudioName) when the media file goes.
const castAudioName = "audio.m4a"

// handleCastAudio serves the sound of a video for a speaker to fetch.
//
// A video on disk is cut once into audio.m4a — the AAC track copied, not
// re-encoded, so it costs a second of ffmpeg and a few megabytes — and served
// from the file with ranges. A video not on disk is proxied to ingest, which
// reads YouTube's own AAC track in bounded pieces, and the download is
// scheduled as pressing play would.
//
// The same answers as /stream for the cases that are not a file: a broadcast
// has no end to play to (422), upstream refusing for good is a conflict (409),
// and an unmounted library writes nothing (CLAUDE.md §4).
func (g *Gateway) handleCastAudio(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	videoID := r.PathValue("id")

	resp, err := g.catalog.GetVideo(ctx, connect.NewRequest(&catalogv1.GetVideoRequest{VideoId: videoID}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	v := resp.Msg.GetVideo()

	if decideLive(v.GetLiveStatus(), v.GetIsLiveNow()) == liveYes || v.GetLiveStatus() == "is_upcoming" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "live"})
		return
	}
	if v.GetMediaState() == catalogv1.MediaState_MEDIA_STATE_UNAVAILABLE {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "video_unavailable"})
		return
	}

	if v.GetMediaState() == catalogv1.MediaState_MEDIA_STATE_READY && v.GetMediaPath() != "" {
		switch checkMedia(g.mediaRoot, v.GetMediaPath()) {
		case mediaPresent:
			g.serveCutAudio(w, r, videoID, filepath.Join(g.mediaRoot, v.GetMediaPath()))
			return
		case mediaMissing:
			if _, err := g.catalog.SetMediaState(ctx, connect.NewRequest(&catalogv1.SetMediaStateRequest{
				VideoId: videoID, MediaState: catalogv1.MediaState_MEDIA_STATE_EVICTED,
			})); err != nil {
				g.logger.Warn("mark missing media evicted", "video", videoID, "error", err)
			} else {
				g.logger.Info("media file missing, marked evicted", "video", videoID, "path", v.GetMediaPath())
			}
		case mediaRootUnavailable:
			g.logger.Warn("media root unavailable", "root", g.mediaRoot, "video", videoID)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "media_root_unavailable"})
			return
		}
	}

	if v.GetSourceUrl() != "" {
		if g.cacheDisabled() {
			go g.fetchSubtitlesOnly(v.GetSourceUrl())
		} else {
			go g.ensureDownload(v.GetSourceUrl(), g.userID(r))
		}
	}
	g.proxyIngestAudio(w, r, videoID)
}

func (g *Gateway) serveCutAudio(w http.ResponseWriter, r *http.Request, videoID, mediaFile string) {
	dst := filepath.Join(filepath.Dir(mediaFile), castAudioName)
	if _, err := os.Stat(dst); err != nil {
		// One ffmpeg per video however many ask at once: a speaker retries, and
		// a phone and a speaker can ask in the same second.
		_, err, _ := g.audioCuts.Do(videoID, func() (any, error) {
			if _, err := os.Stat(dst); err == nil {
				return nil, nil
			}
			// Into a file of its own first and renamed after, so a cut that dies
			// half way never leaves an audio.m4a that looks whole.
			tmp := fmt.Sprintf("%s.%d.part", dst, time.Now().UnixNano())
			defer func() { _ = os.Remove(tmp) }()
			started := time.Now()
			if err := g.extractAudio(context.WithoutCancel(r.Context()), mediaFile, tmp); err != nil {
				return nil, err
			}
			if err := os.Rename(tmp, dst); err != nil {
				return nil, err
			}
			g.logger.Info("cast audio cut", "video", videoID, "took", time.Since(started).Round(time.Millisecond))
			return nil, nil
		})
		if err != nil {
			g.logger.Error("cut cast audio", "video", videoID, "error", err)
			http.Error(w, "cannot prepare audio", http.StatusInternalServerError)
			return
		}
	}
	f, err := os.Open(dst)
	if err != nil {
		http.Error(w, "cannot read audio", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "cannot read audio", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "audio/mp4")
	http.ServeContent(w, r, castAudioName, info.ModTime(), f)
}

// ffmpegExtractAudio copies the sound out of a video file. -c:a copy, because
// every file in the library carries AAC (ffprobed 3 of 3) and a speaker plays
// it as it is; faststart, so a player can begin before reading to the end.
func ffmpegExtractAudio(ctx context.Context, src, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-y", "-i", src,
		"-vn", "-c:a", "copy", "-movflags", "+faststart", "-f", "mp4", dst).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, tail(out, 512))
	}
	return nil
}

func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}

func (g *Gateway) proxyIngestAudio(w http.ResponseWriter, r *http.Request, videoID string) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		g.ingestBaseURL+"/audio/"+url.PathEscape(videoID), nil)
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	if v := r.Header.Get("Range"); v != "" {
		req.Header.Set("Range", v)
	}
	resp, err := g.streamClient.Do(req)
	if err != nil {
		g.logger.Warn("audio proxy", "video", videoID, "error", err)
		http.Error(w, "cannot open audio", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		g.logger.Warn("audio proxy answered", "video", videoID, "status", resp.StatusCode)
	}
	for _, header := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(header); v != "" {
			w.Header().Set(header, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 64*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}
