package ytdlp

import (
	"slices"
	"testing"
)

const (
	videoURL = "https://example.com/video.mp4"
	audioURL = "https://example.com/audio.m4a"
)

// seekBeforeInput reports the -ss value that applies to the given URL: the last
// -ss appearing before its -i. Zero means the input was not seeked at all.
//
// Written this way on purpose. What makes an input seek cheap is that it comes
// *before* -i — after it, ffmpeg reads and discards everything up to the mark,
// which on an hour-long video is minutes of work. Asserting on the flag's
// presence alone would pass for a command that does the expensive thing.
func seekBeforeInput(t *testing.T, args []string, u string) string {
	t.Helper()
	seek := ""
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "-ss":
			seek = args[i+1]
		case "-i":
			if args[i+1] == u {
				return seek
			}
			seek = ""
		}
	}
	t.Fatalf("input %q not found in %v", u, args)
	return ""
}

func TestRemuxArgsSeeksAudioToTheVideosKeyframe(t *testing.T) {
	// The two inputs are seeked separately and do not land in the same place: an
	// input seek snaps to the nearest keyframe at or before the mark, so asking
	// both for 600 puts the video at 597.972 and the audio at 599.980. Measured
	// on a real video, the gap was 2.008s of sound running ahead of picture.
	args := remuxArgs([]string{videoURL, audioURL}, 600, 597.972375)

	if got := seekBeforeInput(t, args, videoURL); got != "600.000" {
		t.Errorf("video seeked to %q, want the mark itself (600.000)", got)
	}
	// The audio is sent to where the video will actually land, not to where the
	// viewer asked. Sending both to 597.972 does not work either: ffmpeg reads
	// that as "at or before" and steps back to the previous keyframe, measured
	// at 593.593 — a worse gap in the other direction.
	if got := seekBeforeInput(t, args, audioURL); got != "597.972" {
		t.Errorf("audio seeked to %q, want the video's keyframe (597.972)", got)
	}
}

func TestRemuxArgsWithoutAKeyframeSeeksBothAlike(t *testing.T) {
	// Zero means the probe could not answer. Sound slightly out of step is worse
	// than a video that will not open, but only just — so it still opens.
	args := remuxArgs([]string{videoURL, audioURL}, 600, 0)

	if got := seekBeforeInput(t, args, videoURL); got != "600.000" {
		t.Errorf("video seeked to %q, want 600.000", got)
	}
	if got := seekBeforeInput(t, args, audioURL); got != "600.000" {
		t.Errorf("audio seeked to %q, want 600.000", got)
	}
}

func TestRemuxArgsFromTheStartSeeksNothing(t *testing.T) {
	// Opening at zero needs no alignment: both inputs begin at the same place on
	// their own, which is why this bug was invisible until seeking existed.
	args := remuxArgs([]string{videoURL, audioURL}, 0, 0)

	if slices.Contains(args, "-ss") {
		t.Errorf("opening at the start should not seek: %v", args)
	}
}

func TestRemuxArgsWithOneInputIgnoresTheAudioMark(t *testing.T) {
	// One URL means the source already offers a muxed file — there is no second
	// stream to align, and the audio mark must not be applied to the only input.
	args := remuxArgs([]string{videoURL}, 600, 597.972375)

	if got := seekBeforeInput(t, args, videoURL); got != "600.000" {
		t.Errorf("single input seeked to %q, want 600.000", got)
	}
}

func TestRemuxArgsCopiesRatherThanReencodes(t *testing.T) {
	// The whole design rests on this: a remux costs no CPU, a transcode costs
	// all of it. See CLAUDE.md §4.
	args := remuxArgs([]string{videoURL, audioURL}, 0, 0)

	i := slices.Index(args, "-c")
	if i < 0 || i+1 >= len(args) || args[i+1] != "copy" {
		t.Errorf("expected -c copy, got %v", args)
	}
}

func TestRemuxArgsWritesFragmentedMP4ThroughAPipe(t *testing.T) {
	// An ordinary MP4 keeps its index at the end and cannot be played while it
	// is still being written; fragmented plays from the first fragment. The even
	// one-second fragments are what keep sound and picture arriving together.
	args := remuxArgs([]string{videoURL, audioURL}, 0, 0)

	for _, want := range [][2]string{
		{"-movflags", "frag_keyframe+empty_moov+default_base_moof"},
		{"-frag_duration", "1000000"},
		{"-f", "mp4"},
	} {
		i := slices.Index(args, want[0])
		if i < 0 || i+1 >= len(args) || args[i+1] != want[1] {
			t.Errorf("expected %s %s, got %v", want[0], want[1], args)
		}
	}
	if args[len(args)-1] != "pipe:1" {
		t.Errorf("expected output on stdout, got %q", args[len(args)-1])
	}
}

// optionBeforeInput reports the value of the named option that applies to the
// given input: the last one appearing before its -i, reset at each -i.
//
// Same shape as seekBeforeInput and for the same reason. -request_size after -i
// is an output option, where it means nothing at all — and a command that reads
// open-ended is one googlevideo answers with a redirect to a host that refuses.
func optionBeforeInput(t *testing.T, args []string, option, u string) string {
	t.Helper()
	value := ""
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case option:
			value = args[i+1]
		case "-i":
			if args[i+1] == u {
				return value
			}
			value = ""
		}
	}
	t.Fatalf("input %q not found in %v", u, args)
	return ""
}

// Every request to googlevideo must be bounded, on every input.
//
// Measured on a real 1080p URL: ffprobe without the option answered "403
// Forbidden", and with it answered in 0.14s. Unbounded, the ingest log carried
// `probe keyframe: exit status 1` then `open remux: EOF` then a 502 for every
// video in the library — CLAUDE.md §4.
func TestRemuxArgsBoundsEveryRequest(t *testing.T) {
	args := remuxArgs([]string{videoURL, audioURL}, 20, 18)

	for _, u := range []string{videoURL, audioURL} {
		if got := optionBeforeInput(t, args, "-request_size", u); got != httpRequestSizeBytes {
			t.Errorf("request_size for %q = %q, want %q", u, got, httpRequestSizeBytes)
		}
		// Probing and header parsing happen before the first ordinary request,
		// so without this the refusal simply moves to the request that opens
		// the stream.
		if got := optionBeforeInput(t, args, "-initial_request_size", u); got != httpRequestSizeBytes {
			t.Errorf("initial_request_size for %q = %q, want %q", u, got, httpRequestSizeBytes)
		}
	}
}

// A response body that ends early carries no error status, so neither
// -reconnect (which covers a connection dropping *before* EOF) nor
// -reconnect_on_http_error reaches it. Traced with -loglevel debug on a real
// audio URL: ffmpeg asks for a megabyte, reads 16384 bytes, and in the failing
// case has nothing more — reported as `partial file` at ~16.5 KB and again at
// the ~163 KB it went on to seek to, the pair of offsets four captured failures
// shared to within 200 bytes across four different videos. Sixteen kilobytes
// less the container header is about 34 AAC frames, or 0.79 seconds, which is
// the number the browser named every time.
func TestRemuxArgsReconnectsWhenABodyEndsEarly(t *testing.T) {
	args := remuxArgs([]string{videoURL, audioURL}, 20, 18)

	for _, u := range []string{videoURL, audioURL} {
		if got := optionBeforeInput(t, args, "-reconnect_at_eof", u); got != "1" {
			t.Errorf("reconnect_at_eof for %q = %q, want \"1\"", u, got)
		}
		// Bounded: at the true end of a finite file this would otherwise ask
		// again for ever, and every ask is counted against this address.
		if got := optionBeforeInput(t, args, "-reconnect_max_retries", u); got == "" {
			t.Errorf("reconnect_max_retries for %q is unset — an unbounded retry at EOF", u)
		}
	}
}
