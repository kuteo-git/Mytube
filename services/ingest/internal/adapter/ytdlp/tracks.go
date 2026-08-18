package ytdlp

import (
	"context"
	"fmt"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// ResolveTracks picks the two adaptive files an HLS playlist describes: one
// carrying only picture, one carrying only sound.
//
// Separate from ResolveRemuxURLs, which answers the same question for ffmpeg
// and needs nothing but URLs. A playlist has to *describe* what it points at —
// the codec string above all, because a player decides whether it can play a
// stream by reading CODECS before fetching a byte of it.
//
// Verified before being handed over, for the same reason and by the same means
// as every other resolve here (verify.go).
func (d *Downloader) ResolveTracks(ctx context.Context, videoURL string, height int32) (domain.MediaTracks, error) {
	if height <= 0 {
		height = 1080
	}

	var lastErr error
	for range resolveAttempts {
		tracks, err := d.resolveTracksOnce(ctx, videoURL, height)
		if err != nil {
			return domain.MediaTracks{}, err
		}
		lastErr = nil
		for _, u := range []string{tracks.Video.URL, tracks.Audio.URL} {
			if probeErr := verifyURL(ctx, u); probeErr != nil {
				lastErr = probeErr
				break
			}
		}
		if lastErr == nil {
			return tracks, nil
		}
		if ctx.Err() != nil {
			return domain.MediaTracks{}, ctx.Err()
		}
	}
	return domain.MediaTracks{}, fmt.Errorf("resolve tracks %q: every resolved url was refused: %w", videoURL, lastErr)
}

func (d *Downloader) resolveTracksOnce(ctx context.Context, videoURL string, height int32) (domain.MediaTracks, error) {
	result, err := newCommand(purposeMedia).
		SkipDownload().
		NoPlaylist().
		DumpJSON().
		NoWarnings().
		Run(ctx, videoURL)
	if err != nil {
		return domain.MediaTracks{}, fmt.Errorf("resolve tracks %q: %w", videoURL, err)
	}
	infos, err := result.GetExtractedInfo()
	if err != nil {
		return domain.MediaTracks{}, err
	}
	if len(infos) == 0 {
		return domain.MediaTracks{}, domain.ErrNotFound
	}

	var video, audio domain.MediaTrack
	for _, f := range infos[0].Formats {
		if f.URL == "" {
			continue
		}
		if protocol := deref(f.Protocol); protocol != "https" && protocol != "http" {
			continue
		}
		vcodec, acodec := deref(f.VCodec), deref(f.ACodec)
		hasVideo := vcodec != "" && vcodec != "none"
		hasAudio := acodec != "" && acodec != "none"

		switch {
		case hasVideo && !hasAudio:
			// H.264 only. A playlist may offer anything the device can decode,
			// and this one is aimed at a television and an iPhone: VP9 and AV1
			// are where those two disagree most.
			if deref(f.Extension) != "mp4" || int32(deref(f.Height)) > height {
				continue
			}
			if video.URL == "" || int(deref(f.Height)) > video.Height {
				video = domain.MediaTrack{
					URL:    f.URL,
					Codec:  vcodec,
					Width:  int(deref(f.Width)),
					Height: int(deref(f.Height)),
					// TBR is the format's own bitrate in kbit/s; the playlist
					// wants bits, and BANDWIDTH is what a player budgets with.
					Bitrate: int(deref(f.TBR) * 1000),
				}
			}
		case hasAudio && !hasVideo:
			if deref(f.Extension) != "m4a" {
				continue
			}
			if bitrate := int(deref(f.TBR) * 1000); audio.URL == "" || bitrate > audio.Bitrate {
				audio = domain.MediaTrack{URL: f.URL, Codec: acodec, Bitrate: bitrate}
			}
		}
	}

	if video.URL == "" || audio.URL == "" {
		return domain.MediaTracks{}, domain.ErrNoProgressiveFormat
	}
	return domain.MediaTracks{Video: video, Audio: audio}, nil
}
