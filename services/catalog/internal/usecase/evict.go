package usecase

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/lucnguyen/local-youtube/services/catalog/internal/domain"
)

// sweepInterval is how often disk usage is checked. Hourly is frequent enough
// that the ceiling is never far away, and rare enough to be invisible.
const sweepInterval = time.Hour

// Evictor keeps the media directory under its budget.
//
// The disk is the hardest constraint in this system: 34 GiB total, roughly 25
// of which is available to media. Everything else in the design assumes
// something is enforcing that, and until now nothing was.
//
// Deleting only the media file — never the catalog row, the thumbnail or the
// history — is what makes eviction reversible. A reclaimed video keeps its
// place in the grid and offers to fetch itself again.
type Evictor struct {
	repo          domain.EvictionRepository
	mediaRoot     string
	highWatermark int64
	lowWatermark  int64
	logger        *slog.Logger
}

func NewEvictor(repo domain.EvictionRepository, mediaRoot string, highWatermark, lowWatermark int64, logger *slog.Logger) *Evictor {
	return &Evictor{
		repo:          repo,
		mediaRoot:     mediaRoot,
		highWatermark: highWatermark,
		lowWatermark:  lowWatermark,
		logger:        logger,
	}
}

func (e *Evictor) Run(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()

	for {
		if freed, err := e.SweepOnce(ctx); err != nil {
			e.logger.Error("eviction sweep", "error", err)
		} else if freed > 0 {
			e.logger.Info("eviction sweep freed space", "bytes", freed)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// SweepOnce deletes least-recently-accessed unpinned media until usage is back
// under the low watermark. Sweeping to a level below the trigger, rather than
// just under it, is what stops the sweep from running again on the next tick.
func (e *Evictor) SweepOnce(ctx context.Context) (int64, error) {
	used, err := e.repo.UsedBytes(ctx)
	if err != nil {
		return 0, err
	}
	if used <= e.highWatermark {
		return 0, nil
	}

	candidates, err := e.repo.ListEvictionCandidates(ctx, e.lowWatermark)
	if err != nil {
		return 0, err
	}

	var freed int64
	for _, c := range candidates {
		if used-freed <= e.lowWatermark {
			break
		}
		if c.MediaPath == "" {
			continue
		}

		path := filepath.Join(e.mediaRoot, c.MediaPath)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			// A file that cannot be removed must not stop the sweep, or one bad
			// path would let the disk fill anyway.
			e.logger.Warn("remove media file", "video", c.VideoID, "path", path, "error", err)
			continue
		}

		if err := e.repo.MarkEvicted(ctx, c.VideoID); err != nil {
			e.logger.Warn("mark evicted", "video", c.VideoID, "error", err)
			continue
		}
		freed += c.SizeBytes
	}
	return freed, nil
}
