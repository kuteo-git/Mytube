package ytdlp

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/lrstanley/go-ytdlp"
)

// RemuxStream is a live 1080p stream assembled on the fly.
//
// It exists because YouTube stopped publishing high-resolution progressive
// formats: the only muxed file on offer is itag 18, capped at 360p. Everything
// above that is adaptive — video and audio in separate files — which a bare
// <video> element cannot play.
//
// So the two streams are muxed here instead, with ffmpeg, into a fragmented
// MP4. Fragmented is the point: an ordinary MP4 keeps its index at the end and
// is unplayable until complete, whereas a fragmented one is playable from the
// first fragment. The copy is a remux, never a re-encode, so this costs
// essentially no CPU — see CLAUDE.md §4, "không transcode" ≠ "không ABR".
//
// The trade-off is seeking: a piped stream has no length and no index, so the
// viewer can only move within what has buffered. That is temporary — the
// background download produces a faststart file on disk, and playback moves to
// it as soon as it lands.
type RemuxStream struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
}

// h264 is requested ahead of newer codecs on purpose. AV1 and VP9 give smaller
// files, but h264 is the one format every browser and television decodes, and
// this has to play on a TV that may be years old.
const remuxFormat = "bestvideo[height<=%d][vcodec^=avc1]+bestaudio[ext=m4a]/" +
	"bestvideo[height<=%d]+bestaudio/best[height<=%d]"

// ResolveRemuxURLs asks yt-dlp for the direct media URLs without downloading
// anything. Two URLs mean adaptive streams to be muxed; one means the source
// already offers a muxed file and no remux is needed.
func (d *Downloader) ResolveRemuxURLs(ctx context.Context, videoURL string, height int32) ([]string, error) {
	if height <= 0 {
		height = 1080
	}

	result, err := ytdlp.New().
		Format(fmt.Sprintf(remuxFormat, height, height, height)).
		GetURL().
		NoPlaylist().
		NoWarnings().
		Run(ctx, videoURL)
	if err != nil {
		return nil, fmt.Errorf("resolve remux urls %q: %w", videoURL, err)
	}

	var urls []string
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "http") {
			urls = append(urls, line)
		}
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("resolve remux urls %q: no media urls", videoURL)
	}
	return urls, nil
}

// OpenRemux starts ffmpeg and returns its output. The caller must Close the
// stream, which is what kills ffmpeg when a viewer navigates away — without
// that, every abandoned video would leave a process pulling bytes forever.
func (d *Downloader) OpenRemux(ctx context.Context, urls []string) (io.ReadCloser, error) {
	args := []string{"-hide_banner", "-loglevel", "error"}
	for _, u := range urls {
		// Reconnect flags matter more here than for a file: these are signed
		// CDN URLs being read for the length of a whole video.
		args = append(args,
			"-reconnect", "1",
			"-reconnect_streamed", "1",
			"-reconnect_delay_max", "5",
			"-i", u)
	}
	args = append(args,
		"-c", "copy",
		// empty_moov makes the file playable before any fragment is finalised;
		// frag_keyframe starts a new fragment at each keyframe so playback can
		// begin at the first one.
		"-movflags", "frag_keyframe+empty_moov+default_base_moof",
		"-f", "mp4", "pipe:1")

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	// ffmpeg reads stdin for interactive commands and would consume the
	// caller's if left attached — the same trap noted in CLAUDE.md §8b.
	cmd.Stdin = nil

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &RemuxStream{cmd: cmd, stdout: stdout}, nil
}

func (s *RemuxStream) Read(p []byte) (int, error) { return s.stdout.Read(p) }

func (s *RemuxStream) Close() error {
	_ = s.stdout.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
	return nil
}
