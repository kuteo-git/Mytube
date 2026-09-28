package ytdlp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"

	"github.com/lrstanley/go-ytdlp"
	"golang.org/x/sync/errgroup"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/adapter/proxycfg"
	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// Storyboards: the strip of stills a scrub bar shows at the time being aimed at.
//
// YouTube publishes these as a ladder of sprite sheets — one JPEG carrying a
// grid of frames — and yt-dlp already extracts the whole ladder as formats with
// `format_note: storyboard`. Nothing here ever read them, which is why scrubbing
// showed a clock and no picture.
//
// ## Why the sprites are copied to the media root rather than linked
//
// The URLs are signed and the signature is mandatory: measured, the same path
// without `sqp`+`sigh` answers **403**, and there is no `expire=` parameter to
// read a lifetime from. So a URL stored in the catalogue is a preview that works
// until an unknown day and then silently stops.
//
// Copying them is the pattern the narration clips already established — files
// under `<mediaRoot>/<videoId>/` served over `/media`. It also means both
// clients draw a preview the same way they draw every other image in the
// library, from a path relative to the media root, and neither ever talks to
// i.ytimg.com.
//
// Measured cost on a ten-minute video at the chosen rung: 6 sprites, ~282 KiB,
// 150 frames.
//
// ## Why the disk is the store and the catalogue knows nothing
//
// A row earns its place in the database when something *queries* by it. Nothing
// does: no ranking, no feed and no listing has an opinion about storyboards, and
// the only reader is one watch screen asking about one video. So the spec lives
// beside the sprites it describes, where the two cannot disagree — a row saying
// "9 sprites" over a directory holding 4 is a fault that this arrangement cannot
// have.
const (
	storyboardDirName  = "storyboard"
	storyboardSpecName = "spec.json"

	// The tile height to aim for, in pixels.
	//
	// YouTube's rungs are 27, 45, 90 and 180 pixels tall whatever the video's
	// aspect ratio — a portrait upload's 90-tall rung is 50 wide and a landscape
	// one's is 160 — so height is what names a rung, and this is the only number
	// that needs choosing.
	//
	// 180, and this number is decided by how large a still is *drawn* rather
	// than by what the sheets cost.
	//
	// It was 90 first, on the measurement that the rung above costs 15 sheets
	// and 750 KiB to carry *fewer* stills (135) than 90's 6 sheets and 282 KiB
	// carry (150) — a true trade, and the wrong one once the phone client drew
	// the still over the whole player instead of in a card on it. A 160-pixel
	// tile filling a 1080-pixel wide picture is a **6.75x** enlargement, which
	// is not a soft preview but a blocky one; 320 makes it 3.4x.
	//
	// The finer grid that 90 buys is not lost either: 180's sheets hold 9 stills
	// apiece against 25, so the *interval* between stills is about the same. What
	// is paid is disk, for videos somebody has actually opened.
	storyboardTargetTileHeight = 180

	// At most this many sprites are fetched at once.
	//
	// They are tens of kilobytes each and a three-hour film has dozens, so
	// running them in turn is most of the wait. Bounded rather than unbounded
	// because this is the same host the thumbnails come from and the library has
	// been blocked once already for asking too fast.
	storyboardFetchLimit = 4
)

// storyboardSpec is what is written beside the sheets, and is deliberately its
// own shape rather than the domain type.
//
// It is a file format: it outlives this process, and the day a field is added to
// `domain.Storyboard` every spec already on disk must still read. Sharing one
// struct between a port and a serialisation is how a rename becomes a library of
// unreadable specs.
type storyboardSpec struct {
	// The size of one frame, in pixels.
	TileWidth  int `json:"tileWidth"`
	TileHeight int `json:"tileHeight"`
	// The grid inside each sheet.
	Rows    int `json:"rows"`
	Columns int `json:"columns"`
	// How much of the video one frame stands for.
	//
	// Derived from a sheet's own duration rather than from the video's, because
	// the last sheet is usually only partly filled: dividing the video's length
	// by the total number of slots would stretch every frame's share by however
	// much of the last sheet is blank.
	IntervalSeconds float64 `json:"intervalSeconds"`
	// The sheets, in order, as paths relative to the media root.
	Sprites []string `json:"sprites"`
}

// storyboardGeometry reads a chosen format's shape, or reports that it has none.
//
// Separate from the picking and the fetching so the arithmetic can be asserted
// without a yt-dlp process or a network — the reason `wholeSeconds` and
// `levelsFor` are named functions in the app that consumes this.
func storyboardGeometry(f *ytdlp.ExtractedFormat) (storyboardSpec, bool) {
	if f == nil {
		return storyboardSpec{}, false
	}
	rows, columns := deref(f.Rows), deref(f.Columns)
	width, height := int(deref(f.Width)), int(deref(f.Height))
	if rows <= 0 || columns <= 0 || width <= 0 || height <= 0 {
		return storyboardSpec{}, false
	}
	if len(f.Fragments) == 0 {
		return storyboardSpec{}, false
	}
	// A sheet that claims no duration cannot say what any of its frames are
	// for. Refused rather than defaulted: a made-up interval draws the wrong
	// scene for every moment, which is worse than drawing nothing.
	first := f.Fragments[0].Duration
	if first <= 0 {
		return storyboardSpec{}, false
	}
	return storyboardSpec{
		TileWidth:       width,
		TileHeight:      height,
		Rows:            rows,
		Columns:         columns,
		IntervalSeconds: first / float64(rows*columns),
	}, true
}

// pickStoryboard chooses which rung of the ladder to copy.
//
// The rule is the smallest rung at least as tall as the target, falling back to
// the tallest there is. Stated that way round on purpose: "closest to 90" reads
// the same on the ladders YouTube actually publishes and picks a 45-tall rung
// over a 180-tall one when a video only has those two, which is a preview nobody
// can read to save bytes nobody was counting.
func pickStoryboard(formats []*ytdlp.ExtractedFormat) *ytdlp.ExtractedFormat {
	var best *ytdlp.ExtractedFormat
	var bestHeight int
	for _, f := range formats {
		if f == nil || deref(f.Protocol) != "mhtml" {
			continue
		}
		if _, ok := storyboardGeometry(f); !ok {
			continue
		}
		height := int(deref(f.Height))
		switch {
		case best == nil:
		// Both reach the target: the shorter one is the cheaper of two that
		// are each good enough.
		case bestHeight >= storyboardTargetTileHeight && height >= storyboardTargetTileHeight:
			if height >= bestHeight {
				continue
			}
		// Only the new one reaches it.
		case height >= storyboardTargetTileHeight:
		// Neither does, so the taller one is the best available.
		default:
			if height <= bestHeight {
				continue
			}
		}
		best, bestHeight = f, height
	}
	return best
}

// Storyboard returns the preview sheets for a video, copying them on first ask.
//
// Guarded on the spec already being on disk, which is `RefreshVideoMetadata`'s
// own shape and for its reason: one call is one full metadata fetch upstream,
// and this library has been blocked once for making too many. A client asking
// again is not a fault; asking YouTube again for a video already copied would
// be.
//
// A video with no storyboard at all is `domain.ErrNotFound` rather than an
// error: a Short too brief to have one, or an upload YouTube has not finished
// processing, is a fact about that video and not a failure of this system.
func (d *Downloader) Storyboard(ctx context.Context, videoURL, videoID string) (domain.Storyboard, error) {
	if videoID == "" {
		return domain.Storyboard{}, fmt.Errorf("%w: video id is required", domain.ErrInvalid)
	}

	dir := filepath.Join(d.mediaRoot, videoID, storyboardDirName)
	specPath := filepath.Join(dir, storyboardSpecName)
	if spec, ok := readStoryboardSpec(specPath); ok {
		return spec.toDomain(), nil
	}

	result, err := newCommand(purposeMedia, proxycfg.Media).
		SkipDownload().
		NoPlaylist().
		DumpJSON().
		NoWarnings().
		Run(ctx, videoURL)
	if err != nil {
		return domain.Storyboard{}, fmt.Errorf("storyboard %q: %w", videoURL, err)
	}
	infos, err := result.GetExtractedInfo()
	if err != nil {
		return domain.Storyboard{}, err
	}
	if len(infos) == 0 {
		return domain.Storyboard{}, domain.ErrNotFound
	}

	chosen := pickStoryboard(infos[0].Formats)
	spec, ok := storyboardGeometry(chosen)
	if !ok {
		return domain.Storyboard{}, domain.ErrNotFound
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return domain.Storyboard{}, err
	}

	// All or nothing, and that is the whole reason this collects into a slice
	// indexed by position rather than appending as each lands. A missing sheet
	// in the middle is a hole in the middle of the scrub bar — every frame past
	// it still has a place to be drawn from, so the preview would be quietly
	// wrong for the rest of the video rather than absent. A missing tail would
	// be honest, but it cannot be told from the middle case here without
	// knowing which sheet failed, and the sheets are cheap enough that
	// refusing the lot and asking again later costs less than the distinction.
	names := make([]string, len(chosen.Fragments))
	group, fetchCtx := errgroup.WithContext(ctx)
	group.SetLimit(storyboardFetchLimit)
	for i, fragment := range chosen.Fragments {
		i, url := i, fragment.URL
		group.Go(func() error {
			name, err := d.saveStoryboardSheet(fetchCtx, url, dir, i)
			if err != nil {
				return err
			}
			names[i] = name
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		// Leave nothing behind claiming to be a storyboard. Without this the
		// next ask reads a spec that was never written, finds the sheets that
		// did land, and draws the wrong scene for every moment after the gap.
		_ = os.RemoveAll(dir)
		return domain.Storyboard{}, fmt.Errorf("storyboard %q: %w", videoID, err)
	}

	spec.Sprites = make([]string, 0, len(names))
	for _, name := range names {
		spec.Sprites = append(spec.Sprites, path.Join(videoID, storyboardDirName, name))
	}

	// Written last, so its presence is the promise that everything it names is
	// already there.
	if blob, err := json.Marshal(spec); err == nil {
		if err := os.WriteFile(specPath, blob, 0o644); err != nil {
			d.logger.Warn("storyboard: spec not written", "video", videoID, "error", err)
		}
	}
	return spec.toDomain(), nil
}

func (s storyboardSpec) toDomain() domain.Storyboard {
	return domain.Storyboard{
		TileWidth:       int32(s.TileWidth),
		TileHeight:      int32(s.TileHeight),
		Rows:            int32(s.Rows),
		Columns:         int32(s.Columns),
		IntervalSeconds: s.IntervalSeconds,
		Sprites:         s.Sprites,
	}
}

// readStoryboardSpec is the guard that keeps a second ask off the network.
//
// A spec naming no sheets, or claiming an interval of zero, is treated as absent
// rather than as an answer: both describe a preview that cannot be drawn, and
// reading one back as valid would make a half-written directory permanent.
func readStoryboardSpec(specPath string) (storyboardSpec, bool) {
	blob, err := os.ReadFile(specPath)
	if err != nil {
		return storyboardSpec{}, false
	}
	var spec storyboardSpec
	if err := json.Unmarshal(blob, &spec); err != nil {
		return storyboardSpec{}, false
	}
	if len(spec.Sprites) == 0 || spec.IntervalSeconds <= 0 {
		return storyboardSpec{}, false
	}
	return spec, true
}

// saveStoryboardSheet copies one sheet and returns the name it was written as.
//
// The extension follows the *bytes*, not the URL. YouTube serves these from a
// path ending `.jpg` and answers `image/webp` — measured, `RIFF....WEBP` in the
// first twelve bytes — and `/media` is an `http.FileServer`, which types a
// response from its extension. Named `.jpg`, every sheet would be declared
// `image/jpeg` to both clients, which is a decoder's problem to discover rather
// than ours to hand it.
func (d *Downloader) saveStoryboardSheet(ctx context.Context, url, dir string, index int) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("storyboard sheet %d: upstream answered %d", index, resp.StatusCode)
	}

	blob, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%d%s", index, storyboardSheetExtension(blob))
	if err := os.WriteFile(filepath.Join(dir, name), blob, 0o644); err != nil {
		return "", err
	}
	return name, nil
}

// storyboardSheetExtension names a sheet after what is actually in it.
func storyboardSheetExtension(blob []byte) string {
	switch {
	case len(blob) >= 12 && bytes.Equal(blob[0:4], []byte("RIFF")) && bytes.Equal(blob[8:12], []byte("WEBP")):
		return ".webp"
	case len(blob) >= 8 && bytes.Equal(blob[0:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return ".png"
	default:
		return ".jpg"
	}
}
