package ytdlp

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
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
	stderr *tailBuffer
}

// Stderr reports what ffmpeg complained about, or "" when it said nothing.
//
// It exists because a mux that fails does so by writing no bytes, and the
// caller then reports `EOF` — a word that describes the pipe rather than the
// fault. Every remux failure in the log read `open remux ... error=EOF` while
// ffmpeg was, a few kilobytes away, saying "Server returned 403 Forbidden".
func (s *RemuxStream) Stderr() string { return s.stderr.String() }

// tailBuffer keeps the last few kilobytes written to it and drops the rest.
//
// ffmpeg is asked for `-loglevel error`, so this is normally a line or two —
// but a stream that fails mid-play can repeat one every second for an hour, and
// this is held for as long as the process lives.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const stderrTailBytes = 4096

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > stderrTailBytes {
		t.buf = t.buf[len(t.buf)-stderrTailBytes:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

// h264 is requested ahead of newer codecs on purpose. AV1 and VP9 give smaller
// files, but h264 is the one format every browser and television decodes, and
// this has to play on a TV that may be years old.
const remuxFormat = "bestvideo[height<=%d][vcodec^=avc1]+bestaudio[ext=m4a]/" +
	"bestvideo[height<=%d]+bestaudio/best[height<=%d]"

// How much of a media file ffmpeg and ffprobe may ask for in one request.
//
// **Never let either of them read open-ended.** CLAUDE.md §4 records this for
// the instant tier, where the gateway does its own fetching and could obey it;
// ffmpeg does its own HTTP and, left alone, asks for the rest of the file. That
// request is answered with a redirect to a host that then refuses — measured on
// a real 1080p URL:
//
//	curl -r 0-1048575  → 206
//	curl -r 0-         → 302, and the host it points at → 403
//	ffprobe, no option → 403 Forbidden, exit 1
//	ffprobe -request_size 2M → the answer, in 0.14s
//
// It is the whole of `probe keyframe: exit status 1` followed by `open remux:
// EOF` followed by a 502, which the ingest log carried for **every video** —
// the mux only ever opened when the retry happened to be let through.
//
// **1 MiB, lowered from 2 MiB, because 2 MiB was sitting on the line rather
// than under it.** The audio track is where this bites and the video track is
// not, which is why the log read `in#1 … 403 Forbidden` — in#1 being the second
// input — for every video while the picture would have opened perfectly.
// Measured on four videos, both tracks, fresh URLs:
//
//	video track: 512 KiB, 1 MiB, 2 MiB, 4 MiB → 206 every time
//	audio track: 512 KiB → 206, 1 MiB → 206, 2 MiB → 403, 4 MiB → 403  (8 of 8)
//
// And on the same URL, seconds apart: `ffprobe -request_size 2M` → refused,
// `-request_size 1M` → the duration in a fraction of a second.
//
// The lower limit belongs to the same experiment that verify.go is about: the
// audio track of a resolve marked `fexp=…51946838` refuses anything above a
// megabyte, while a resolve marked `…51946837` serves 2 MiB happily. So this is
// not a fixed property of googlevideo to be tuned up to — it is a line that
// moves, and the number has to stay well below wherever it currently is.
//
// The same figure is the instant tier's `instantChunkBytes` and the size
// verify.go probes with, deliberately: one number, and the probe asks upstream
// exactly what the readers will go on to ask. Do not raise it without measuring
// both tracks again.
const httpRequestSizeBytes = "1048576"

// bufferedHTTP is the option pair that bounds every request ffmpeg makes.
// `initial_request_size` covers probing and header parsing, which happens before
// the first one and would otherwise be the request that gets refused.
func bufferedHTTP() []string {
	return []string{
		"-request_size", httpRequestSizeBytes,
		"-initial_request_size", httpRequestSizeBytes,
	}
}

// ResolveRemuxURLs asks yt-dlp for the direct media URLs without downloading
// anything. Two URLs mean adaptive streams to be muxed; one means the source
// already offers a muxed file and no remux is needed.
// Verified before being handed over, and resolved again when refused — the
// same rule and the same measurements as the instant tier (verify.go). The mux
// is where an unverified URL cost the most: ffmpeg opening a refused URL writes
// no bytes, the caller can only report that as `EOF`, and the browser turns it
// into DEMUXER_ERROR_COULD_NOT_OPEN. Every 502 the player logged for a remux
// began here.
func (d *Downloader) ResolveRemuxURLs(ctx context.Context, videoURL string, height int32) ([]string, error) {
	var lastErr error
	for range resolveAttempts {
		urls, err := d.resolveRemuxURLsOnce(ctx, videoURL, height)
		if err != nil {
			return nil, err
		}
		// Every one of them, because the mux reads both tracks and one refused
		// input fails the whole stream just as surely as two would.
		lastErr = nil
		for _, u := range urls {
			if probeErr := verifyURL(ctx, u); probeErr != nil {
				lastErr = probeErr
				break
			}
		}
		if lastErr == nil {
			return urls, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("resolve remux urls %q: every resolved url was refused: %w", videoURL, lastErr)
}

func (d *Downloader) resolveRemuxURLsOnce(ctx context.Context, videoURL string, height int32) ([]string, error) {
	if height <= 0 {
		height = 1080
	}

	result, err := newCommand(purposeMedia).
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
	args := append([]string{"-v", "error"}, bufferedHTTP()...)
	args = append(args,
		"-read_intervals", interval,
		"-select_streams", "v:0",
		"-show_entries", "packet=pts_time",
		"-of", "csv=p=0",
		videoURL)
	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	cmd.Stdin = nil

	// Kept, because "exit status 1" says only that ffprobe was unhappy. What it
	// writes here is the difference between a URL upstream refused and a video
	// with no keyframe where one was asked for, and those want opposite answers.
	// Run rather than Output: the latter refuses to run at all once Stderr is
	// set, and stderr is the whole point.
	var stdout strings.Builder
	var stderr tailBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("probe keyframe %.3f: %w: %s", at, err, stderr.String())
	}
	line := strings.TrimSpace(stdout.String())
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
		// Every request bounded — see httpRequestSizeBytes. Per input and before
		// -i, like every other input option: after -i they would be output
		// options and silently do nothing, which is exactly the shape of failure
		// this is here to fix.
		args = append(args, bufferedHTTP()...)
		// Reconnect flags matter more here than for a file: these are signed
		// CDN URLs being read for the length of a whole video.
		args = append(args,
			"-reconnect", "1",
			"-reconnect_streamed", "1",
			// **A refusal is not a network error, and the flags above do not
			// cover it.** `-reconnect` recovers from a dropped connection and an
			// early EOF; a server that answers 403 has answered, so ffmpeg takes
			// the answer and stops — which it reports as `partial file`, the
			// exact words in the one captured failure, on both inputs at once.
			//
			// Every measurement today says that answer is worth asking again:
			// the same URL has gone 206, then 403, then 206 inside an hour, and
			// a chunk request part way through a file is no different from the
			// first one. Without this, one refused chunk out of a hundred ends
			// the input, and an input that ends early while the other carries on
			// is a video whose sound stops after a second.
			"-reconnect_on_http_error", "403,5xx",
			"-reconnect_on_network_error", "1",
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
	// Collected rather than discarded: a mux that fails produces no bytes, and
	// the caller can only report that as EOF. The reason is here.
	stderr := &tailBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &RemuxStream{cmd: cmd, stdout: stdout, stderr: stderr}, nil
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
