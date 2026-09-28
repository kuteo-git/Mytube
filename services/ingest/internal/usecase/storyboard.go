package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// VideoStoryboard copies one video's scrub-preview sheets, or answers with the
// ones already copied.
//
// Beside RefreshVideoMetadata rather than inside it, though both are "one video
// somebody has just opened, fetched again from upstream". Folding them together
// was considered and refused: a description is wanted by every video that lacks
// one, while a preview is wanted only by a video somebody is actually scrubbing,
// and the sheets cost a few hundred kilobytes of disk apiece. Joined, opening a
// video would copy sheets nobody was going to look at, and the guard that keeps
// each of them off the network is a different guard — a row's description
// against a directory on disk.
//
// The address is read from the row, which is RefreshVideoMetadata's reasoning
// and worth repeating because it is the one way this could corrupt a library:
// pointing a fetch at one video and writing the answer under another id. A local
// lookup makes it impossible.
func (i *Ingest) VideoStoryboard(ctx context.Context, videoID string) (domain.Storyboard, error) {
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return domain.Storyboard{}, fmt.Errorf("%w: video_id is required", domain.ErrInvalid)
	}

	sourceURL, err := i.library.SourceURLFor(ctx, videoID)
	if err != nil {
		return domain.Storyboard{}, err
	}
	if sourceURL == "" {
		sourceURL = "https://www.youtube.com/watch?v=" + videoID
	}

	return i.downloader.Storyboard(ctx, sourceURL, videoID)
}
