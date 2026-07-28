package usecase

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/lucnguyen/local-youtube/services/catalog/internal/domain"
)

type fakeEvictionRepo struct {
	usedBytes  int64
	candidates []domain.EvictionCandidate
	evicted    []string
}

func (f *fakeEvictionRepo) UsedBytes(context.Context) (int64, error) { return f.usedBytes, nil }

func (f *fakeEvictionRepo) ListEvictionCandidates(context.Context, int64) ([]domain.EvictionCandidate, error) {
	return f.candidates, nil
}

func (f *fakeEvictionRepo) MarkEvicted(_ context.Context, videoID string) error {
	f.evicted = append(f.evicted, videoID)
	return nil
}

func TestSweepDeletesLeastRecentlyAccessedUntilUnderTheLowWatermark(t *testing.T) {
	root := t.TempDir()
	// Three files of 100 bytes each, ordered oldest-accessed first by the repo.
	for _, name := range []string{"v1", "v2", "v3"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "1080p.mp4"), make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	repo := &fakeEvictionRepo{
		usedBytes: 300,
		candidates: []domain.EvictionCandidate{
			{VideoID: "v1", MediaPath: "v1/1080p.mp4", SizeBytes: 100},
			{VideoID: "v2", MediaPath: "v2/1080p.mp4", SizeBytes: 100},
			{VideoID: "v3", MediaPath: "v3/1080p.mp4", SizeBytes: 100},
		},
	}

	// Over the 250 high watermark; delete down to 150.
	evictor := NewEvictor(repo, root, 250, 150, slog.New(slog.NewTextHandler(io.Discard, nil)))

	freed, err := evictor.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if freed != 200 {
		t.Fatalf("freed = %d, want 200", freed)
	}
	if len(repo.evicted) != 2 || repo.evicted[0] != "v1" || repo.evicted[1] != "v2" {
		t.Fatalf("evicted = %v, want [v1 v2] — least recently accessed first", repo.evicted)
	}

	// The media file goes; nothing else does.
	if _, err := os.Stat(filepath.Join(root, "v1", "1080p.mp4")); !os.IsNotExist(err) {
		t.Error("v1 media file survived the sweep")
	}
	if _, err := os.Stat(filepath.Join(root, "v3", "1080p.mp4")); err != nil {
		t.Error("v3 was deleted even though the sweep had already reached the low watermark")
	}
}

func TestSweepDoesNothingBelowTheHighWatermark(t *testing.T) {
	repo := &fakeEvictionRepo{usedBytes: 100}
	evictor := NewEvictor(repo, t.TempDir(), 250, 150, slog.New(slog.NewTextHandler(io.Discard, nil)))

	freed, err := evictor.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if freed != 0 || len(repo.evicted) != 0 {
		t.Fatalf("swept below the watermark: freed=%d evicted=%v", freed, repo.evicted)
	}
}
