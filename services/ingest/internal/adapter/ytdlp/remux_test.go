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
