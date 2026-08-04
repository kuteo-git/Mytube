package ytdlp

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
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

// ProbeKeyframe reports the video timestamp an input seek to `at` will really
// land on: the first packet at or before that mark, which for a video stream is
// always a keyframe. Zero means the question could not be answered.
//
// It exists because the two inputs are seeked separately and do not land in the
// same place — see OpenRemux. Measured at 1.29s against a resolved YouTube URL,
// which is why it is asked only when the stream is being opened part way in.
func (d *Downloader) ProbeKeyframe(ctx context.Context, videoURL string, at float64) (float64, error) {
	// `%+#1` reads one packet from the interval and stops, so this fetches a few
	// hundred kilobytes rather than walking the file.
	interval := strconv.FormatFloat(at, 'f', 3, 64) + "%+#1"
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-read_intervals", interval,
		"-select_streams", "v:0",
		"-show_entries", "packet=pts_time",
		"-of", "csv=p=0",
		videoURL)
	cmd.Stdin = nil

	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("probe keyframe %.3f: %w", at, err)
	}
	line := strings.TrimSpace(string(out))
	line = strings.TrimSuffix(strings.TrimSpace(strings.Split(line, "\n")[0]), ",")
	pts, err := strconv.ParseFloat(line, 64)
	if err != nil {
		return 0, fmt.Errorf("probe keyframe %.3f: unreadable pts %q", at, line)
	}
	return pts, nil
}

// remuxArgs builds the ffmpeg command line. Split out from OpenRemux so the flag
// order — which is the whole of the correctness here — can be tested without
// running ffmpeg.
//
// startSeconds is where the video should begin and audioStartSeconds where the
// audio should. They differ on purpose: see OpenRemux.
func remuxArgs(urls []string, startSeconds, audioStartSeconds float64) []string {
	args := []string{"-hide_banner", "-loglevel", "error"}
	for i, u := range urls {
		// The first input is the video, the second (when there is one) the audio.
		seekTo := startSeconds
		if i > 0 && audioStartSeconds > 0 {
			seekTo = audioStartSeconds
		}
		if seekTo > 0 {
			// Per input, and before -i. After -i it becomes an output seek:
			// ffmpeg would read and throw away everything up to the mark, which
			// on an hour-long video is minutes of work for a viewer waiting.
			args = append(args, "-ss", strconv.FormatFloat(seekTo, 'f', 3, 64))
		}
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
		// The fragmented muxer rebases every track to zero by its own first
		// packet, whatever these say — measured, see OpenRemux. So they cannot
		// align anything; they only keep the muxer from inserting a delay of its
		// own between the two streams. Alignment is done by seeking the inputs
		// to the same content time, above.
		"-avoid_negative_ts", "make_zero",
		"-muxdelay", "0",
		"-muxpreload", "0",
		// empty_moov makes the file playable before any fragment is finalised;
		// frag_keyframe starts a new fragment at each keyframe so playback can
		// begin at the first one.
		"-movflags", "frag_keyframe+empty_moov+default_base_moof",
		// Cut a fragment every second regardless of where keyframes fall.
		//
		// Keyframes alone leave them long and irregular — measured between 1.9
		// and 4.9 seconds on real material — and a browser reading a growing
		// file progressively has to wait for a whole fragment before it can
		// present any of it. Short, regular fragments keep audio and video
		// arriving together instead of in uneven blocks.
		"-frag_duration", "1000000",
		"-f", "mp4", "pipe:1")
	return args
}

// OpenRemux starts ffmpeg and returns its output. The caller must Close the
// stream, which is what kills ffmpeg when a viewer navigates away — without
// that, every abandoned video would leave a process pulling bytes forever.
//
// startSeconds is where the stream should begin. Seeking works by opening a
// fresh mux from a new offset, because a piped fragmented MP4 carries no index
// for a player to seek within — see CLAUDE.md §8b. `-ss` before `-i` makes
// ffmpeg do it as an HTTP range request rather than by decoding and discarding,
// which is what keeps it about as cheap as opening at zero.
//
// audioStartSeconds is where the *audio* should begin, and it is not the same
// number. An input seek lands on the nearest keyframe at or before the mark, so
// asking both inputs for the same second puts the video some way earlier than
// the audio — measured at 2.008s on a real video seeked to 600s. The muxer then
// pulls each track down to zero independently, which erases the difference
// instead of preserving it, and the result is sound running ahead of picture by
// exactly that gap.
//
// No combination of timestamp flags fixes this: -copyts, -start_at_zero,
// -avoid_negative_ts disabled and aresample=async were all measured and all
// collapse to the same output, because fragmented MP4 rebases per track. The
// same inputs written to an ordinary MP4 keep the gap correctly, which is how
// the muxer was identified as the cause. The only fix is to hand the two inputs
// the same content time, which is what ProbeKeyframe is for.
//
// Passing 0 means "do not know" and falls back to seeking both inputs alike:
// sound out of step is worse than a video that will not open, but only just.
func (d *Downloader) OpenRemux(
	ctx context.Context, urls []string, startSeconds, audioStartSeconds float64,
) (io.ReadCloser, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", remuxArgs(urls, startSeconds, audioStartSeconds)...)
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
