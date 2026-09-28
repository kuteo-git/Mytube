package ytdlp

import (
	"math"
	"testing"

	"github.com/lrstanley/go-ytdlp"
)

// Sheets as yt-dlp reports them, so a case reads like the thing it stands for.
func sheet(id string, width, height, rows, columns int, fragments int, fragmentSeconds float64) *ytdlp.ExtractedFormat {
	f := &ytdlp.ExtractedFormat{}
	id, protocol := id, "mhtml"
	f.FormatID, f.Protocol = &id, &protocol
	w, h := float64(width), float64(height)
	r, c := rows, columns
	f.Width, f.Height, f.Rows, f.Columns = &w, &h, &r, &c
	for range fragments {
		f.Fragments = append(f.Fragments, &ytdlp.ExtractedFragment{
			URL:      "https://i.ytimg.com/sb/x/storyboard3_L/M0.jpg?sqp=a&sigh=b",
			Duration: fragmentSeconds,
		})
	}
	return f
}

// The ladders are the ones the running extractor actually answered with, not
// invented ones — the rule `StreamMappingTest` was written under, for the same
// reason: three tiers had been declared in a DTO and never read, and inventing
// the shape is how a mapper passes a test on a path the real answer never takes.
//
// Measured 2026-09-27 with yt-dlp 2026.08.17.
func landscapeLadder() []*ytdlp.ExtractedFormat {
	// Big Buck Bunny, 3840x2160, 635s.
	return []*ytdlp.ExtractedFormat{
		sheet("sb3", 48, 27, 10, 10, 1, 635),
		sheet("sb2", 80, 45, 10, 10, 2, 496.09),
		sheet("sb1", 160, 90, 5, 5, 6, 124.02),
		sheet("sb0", 320, 180, 3, 3, 15, 44.65),
	}
}

func portraitLadder() []*ytdlp.ExtractedFormat {
	// gEWF0LL4IPA, 144x256, 141s. The rungs are the same *heights* as the
	// landscape ladder's and different widths, which is what makes height the
	// thing the picker reads.
	return []*ytdlp.ExtractedFormat{
		sheet("sb2", 25, 45, 10, 10, 1, 141),
		sheet("sb3", 48, 27, 10, 10, 1, 141),
		sheet("sb1", 50, 90, 5, 5, 3, 48.958333),
		sheet("sb0", 101, 180, 3, 3, 8, 17.625),
	}
}

// Which rung is copied.
func TestPickStoryboard(t *testing.T) {
	cases := []struct {
		name    string
		formats []*ytdlp.ExtractedFormat
		want    string
	}{
		{"a full landscape ladder gives the 180-tall rung", landscapeLadder(), "sb0"},
		{"a full portrait ladder gives the 180-tall rung too", portraitLadder(), "sb0"},
		{
			// The rung above the target is not "nearly right", it is the one
			// that is good enough; a taller one is only taken when nothing
			// reaches the target at all.
			"only a rung above the target, so that one",
			[]*ytdlp.ExtractedFormat{sheet("sbX", 640, 360, 2, 2, 30, 20)},
			"sbX",
		},
		{
			// The case a "closest to 90" rule gets wrong: it would take 45 over
			// 180 and draw a preview nobody can read.
			"nothing reaches the target, so the tallest there is",
			[]*ytdlp.ExtractedFormat{
				sheet("sb3", 48, 27, 10, 10, 1, 635),
				sheet("sb2", 80, 45, 10, 10, 2, 496.09),
				sheet("sb1", 160, 90, 5, 5, 6, 124.02),
			},
			"sb1",
		},
		{"exactly the target is taken over anything above it", []*ytdlp.ExtractedFormat{
			sheet("sbX", 640, 360, 2, 2, 30, 20),
			sheet("sb0", 320, 180, 3, 3, 15, 44.65),
		}, "sb0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickStoryboard(tc.formats)
			if got == nil {
				t.Fatalf("picked nothing, want %s", tc.want)
			}
			if deref(got.FormatID) != tc.want {
				t.Fatalf("picked %s, want %s", deref(got.FormatID), tc.want)
			}
		})
	}
}

// What must not be mistaken for a storyboard.
func TestPickStoryboardRefusals(t *testing.T) {
	video := &ytdlp.ExtractedFormat{}
	id, https := "137", "https"
	video.FormatID, video.Protocol = &id, &https
	w, h := 1920.0, 1080.0
	video.Width, video.Height = &w, &h

	cases := []struct {
		name    string
		formats []*ytdlp.ExtractedFormat
	}{
		{"no formats at all", nil},
		// The real reason nothing ever drew a preview: every rung is `mhtml`,
		// and the ladder resolver drops anything that is not direct http. A
		// picker that read the same formats without checking the protocol would
		// hand a video track to the sheet fetcher.
		{"an ordinary video track is not a sheet", []*ytdlp.ExtractedFormat{video}},
		{"a sheet with no grid", []*ytdlp.ExtractedFormat{sheet("sb1", 160, 90, 0, 0, 6, 124.02)}},
		{"a sheet with no fragments", []*ytdlp.ExtractedFormat{sheet("sb1", 160, 90, 5, 5, 0, 124.02)}},
		{"a sheet claiming no duration", []*ytdlp.ExtractedFormat{sheet("sb1", 160, 90, 5, 5, 6, 0)}},
		{"a nil entry", []*ytdlp.ExtractedFormat{nil}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickStoryboard(tc.formats); got != nil {
				t.Fatalf("picked %s, want nothing", deref(got.FormatID))
			}
		})
	}
}

// How much of the video one frame stands for.
//
// The arithmetic that decides which still a moment is drawn from, so it is
// asserted against the numbers the extractor gave rather than against itself.
func TestStoryboardGeometry(t *testing.T) {
	spec, ok := storyboardGeometry(pickStoryboard(landscapeLadder()))
	if !ok {
		t.Fatal("no geometry from a ladder that has one")
	}
	if spec.TileWidth != 320 || spec.TileHeight != 180 {
		t.Fatalf("tile %dx%d, want 320x180", spec.TileWidth, spec.TileHeight)
	}
	if spec.Rows != 3 || spec.Columns != 3 {
		t.Fatalf("grid %dx%d, want 3x3", spec.Rows, spec.Columns)
	}
	// 44.65 seconds spread over 9 slots.
	if math.Abs(spec.IntervalSeconds-4.9611) > 0.0001 {
		t.Fatalf("interval %v, want 4.9611", spec.IntervalSeconds)
	}

	// The interval comes from a *sheet's* duration and not from the video's.
	// Big Buck Bunny is 635s and the fifteen sheets hold 135 slots, so dividing
	// the video by the slots gives 4.70 — which would have every still past the
	// first standing for the wrong moment, drifting further the longer somebody
	// scrubs.
	if wrong := 635.0 / 135.0; math.Abs(spec.IntervalSeconds-wrong) < 0.2 {
		t.Fatalf("interval %v is indistinguishable from the video-length mistake %v", spec.IntervalSeconds, wrong)
	}
}

// A sheet is named after what is in it, because `/media` types a file by its
// extension and YouTube answers `image/webp` from a path ending `.jpg`.
func TestStoryboardSheetExtension(t *testing.T) {
	webp := append([]byte("RIFF"), append([]byte{0, 0, 0, 0}, []byte("WEBPVP8 ")...)...)
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0}
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0, 0, 0, 0, 0, 0}

	cases := []struct {
		name string
		blob []byte
		want string
	}{
		{"what YouTube actually serves", webp, ".webp"},
		{"a png", png, ".png"},
		{"a jpeg", jpeg, ".jpg"},
		{"something too short to tell", []byte{0xFF}, ".jpg"},
		// RIFF alone is not WebP — it is also WAV, which this library writes a
		// great many of for narration.
		{"RIFF that is not WebP", append([]byte("RIFF"), []byte{0, 0, 0, 0, 'W', 'A', 'V', 'E'}...), ".jpg"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := storyboardSheetExtension(tc.blob); got != tc.want {
				t.Fatalf("named %s, want %s", got, tc.want)
			}
		})
	}
}
