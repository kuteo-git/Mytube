package usecase

import (
	"context"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// Scrub-preview sheets, for the fake downloaders that have no opinion about them.
//
// Gathered here rather than spread across the eight files that declare these
// types, because that is the fact worth recording: not one of those tests is
// about storyboards, and eight identical stubs sitting beside eight unrelated
// tests would each look like a decision somebody made there.
//
// They all answer ErrNotFound, which is the honest stub — it is what the real
// adapter answers for a video with no ladder, so a test that accidentally
// reaches one takes the path a Short takes rather than a path nothing takes.
//
// That these had to be written at all is the port doing its job: a method added
// to domain.Downloader cannot be quietly forgotten by an implementation, which
// is the same guarantee §7 buys for translations by making the dictionary an
// interface.

func (*categoryDownloader) Storyboard(context.Context, string, string) (domain.Storyboard, error) {
	return domain.Storyboard{}, domain.ErrNotFound
}

func (*deepenDownloader) Storyboard(context.Context, string, string) (domain.Storyboard, error) {
	return domain.Storyboard{}, domain.ErrNotFound
}

func (*failingDownloader) Storyboard(context.Context, string, string) (domain.Storyboard, error) {
	return domain.Storyboard{}, domain.ErrNotFound
}

func (*fakeDownloader) Storyboard(context.Context, string, string) (domain.Storyboard, error) {
	return domain.Storyboard{}, domain.ErrNotFound
}

func (*previewDownloader) Storyboard(context.Context, string, string) (domain.Storyboard, error) {
	return domain.Storyboard{}, domain.ErrNotFound
}

func (*recheckDownloader) Storyboard(context.Context, string, string) (domain.Storyboard, error) {
	return domain.Storyboard{}, domain.ErrNotFound
}

func (*refusingDownloader) Storyboard(context.Context, string, string) (domain.Storyboard, error) {
	return domain.Storyboard{}, domain.ErrNotFound
}

func (*refusingOnceDownloader) Storyboard(context.Context, string, string) (domain.Storyboard, error) {
	return domain.Storyboard{}, domain.ErrNotFound
}
