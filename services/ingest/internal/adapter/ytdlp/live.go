package ytdlp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/adapter/proxycfg"
	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// ResolveLive lists the HLS playlists of a broadcast still in progress.
//
// A live video publishes nothing the adaptive path can use. Measured on
// `iipR5yUp36o` (ABC News Live) while it was on air: **every** format is
// `m3u8_native` — five video-only renditions from 144p to 720p and two
// sound-only ones — and not a single `https` file. `resolveTracksOnce` filters
// on exactly that protocol, so a live video resolves to nothing and comes back
// as "no directly playable format available".
//
// What is there instead is HLS, which the player already speaks. There is no
// master playlist among them, so whoever builds the one the browser sees has to
// put the sound and the pictures together.
func (d *Downloader) ResolveLive(
	ctx context.Context, videoURL string, maxHeight int32,
) (domain.LiveStream, error) {
	if maxHeight <= 0 {
		maxHeight = 1080
	}

	result, err := newCommand(purposeMedia, proxycfg.Media).
		SkipDownload().
		NoPlaylist().
		DumpJSON().
		NoWarnings().
		Run(ctx, videoURL)
	if err != nil {
		return domain.LiveStream{}, fmt.Errorf("resolve live %q: %w", videoURL, err)
	}
	infos, err := result.GetExtractedInfo()
	if err != nil {
		return domain.LiveStream{}, err
	}
	if len(infos) == 0 {
		return domain.LiveStream{}, domain.ErrNotFound
	}
	info := infos[0]

	out := domain.LiveStream{IsLive: isStillBroadcasting(info)}
	if !out.IsLive {
		// Not an error: the caller asked whether this is live, and it is not.
		// A finished broadcast is an ordinary video and every other path here
		// already knows what to do with one.
		return out, nil
	}

	for _, f := range info.Formats {
		if f.URL == "" || deref(f.Protocol) != "m3u8_native" {
			continue
		}
		vcodec, acodec := deref(f.VCodec), deref(f.ACodec)
		hasVideo := vcodec != "" && vcodec != "none"

		if !hasVideo {
			// yt-dlp leaves acodec blank on YouTube's HLS audio playlists
			// rather than naming it, so "has no picture" is the only reliable
			// test for one.
			out.Renditions = append(out.Renditions, domain.LiveRendition{
				URL: f.URL, Codec: acodec, AudioOnly: true,
				Bitrate: int(deref(f.TBR) * 1000),
			})
			continue
		}
		// H.264 only, the same rule the recorded ladder follows: this is aimed
		// at a television and an iPhone, and VP9 and AV1 are where those two
		// disagree most.
		if !strings.HasPrefix(vcodec, "avc1") || int32(deref(f.Height)) > maxHeight {
			continue
		}
		out.Renditions = append(out.Renditions, domain.LiveRendition{
			URL: f.URL, Codec: vcodec,
			Width: int(deref(f.Width)), Height: int(deref(f.Height)),
			Bitrate: int(deref(f.TBR) * 1000),
		})
	}

	// Tallest first, sound last, so the caller can read the ladder in order and
	// find the audio group at the end.
	sort.SliceStable(out.Renditions, func(i, j int) bool {
		a, b := out.Renditions[i], out.Renditions[j]
		if a.AudioOnly != b.AudioOnly {
			return !a.AudioOnly
		}
		return a.Height > b.Height
	})
	return out, nil
}
