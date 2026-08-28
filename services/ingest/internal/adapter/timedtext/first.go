package timedtext

import (
	"context"
	"log/slog"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// captionSource is the one thing both ways of fetching captions have in common.
type captionSource interface {
	FetchSubtitles(ctx context.Context, videoURL, videoID string, height int32) ([]domain.SubtitleTrack, bool)
}

// First tries three ways of getting captions, cheapest and nearest first.
//
//  1. the local player call — one request to list, one to take;
//  2. another machine, if the household has configured one;
//  3. yt-dlp.
//
// Each is there for a different failure. The local path is right nearly always
// and reads YouTube's own player response, a shape nobody promised will stay
// the same. yt-dlp is behind it because keeping up with that shape is that
// project's whole business — the day it changes, captions must go on working
// rather than stop for everybody until somebody notices.
//
// The remote sits between them because it answers the one failure neither of
// the others can: YouTube refusing this **address**. Measured on 2026-08-27,
// that lasted thirteen hours while videos played normally throughout. Both
// local paths leave from the same front door, so when it is shut they are shut
// together; another machine is another door.
//
// The order is the point twice over. yt-dlp costs four hits on the endpoint
// that is refusing us, so it is what happens last, never first. And a refusal
// **skips** it: asking the same endpoint four more times in the same minute
// gets refused too, louder.
type First struct {
	cheap    captionSource
	remote   captionSource
	fallback captionSource
	logger   *slog.Logger
}

// NewFirst wires the three. `remote` may be nil, which is the ordinary state:
// nobody has to run a second machine.
func NewFirst(cheap, remote, fallback captionSource, logger *slog.Logger) *First {
	return &First{cheap: cheap, remote: remote, fallback: fallback, logger: logger}
}

func (f *First) FetchSubtitles(ctx context.Context, videoURL, videoID string, height int32) ([]domain.SubtitleTrack, bool) {
	tracks, refused := f.cheap.FetchSubtitles(ctx, videoURL, videoID, height)
	if len(tracks) > 0 {
		return tracks, false
	}

	// The refusal is exactly what the other machine is for, so it is tried
	// *before* the refusal is passed on rather than after everything else has
	// failed. An unconfigured remote answers nothing and costs nothing.
	if f.remote != nil {
		if remoteTracks, _ := f.remote.FetchSubtitles(ctx, videoURL, videoID, height); len(remoteTracks) > 0 {
			f.logger.Info("captions came from the other machine", "video", videoID)
			return remoteTracks, false
		}
	}

	// Still refused, and nobody else could help. Not yt-dlp: it leaves by the
	// same door. The retry table waits the block out.
	if refused {
		return nil, true
	}

	f.logger.Info("captions: falling back to yt-dlp", "video", videoID)
	return f.fallback.FetchSubtitles(ctx, videoURL, videoID, height)
}
