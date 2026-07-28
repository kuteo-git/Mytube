package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

type stubChannels struct {
	uploads  domain.ChannelUploads
	err      error
	resolved []string
}

func (s *stubChannels) ResolveChannelID(_ context.Context, channel string) (string, error) {
	s.resolved = append(s.resolved, channel)
	if s.err != nil {
		return "", s.err
	}
	return "UC123", nil
}

func (s *stubChannels) ChannelUploads(context.Context, string, string) (domain.ChannelUploads, error) {
	if s.err != nil {
		return domain.ChannelUploads{}, s.err
	}
	return s.uploads, nil
}

type countingDownloader struct {
	deepenDownloader
	listCalls int
}

func (d *countingDownloader) ListPlaylist(_ context.Context, _ string, _, _ int32) (string, []domain.ExternalVideo, error) {
	d.listCalls++
	return "A", []domain.ExternalVideo{
		{ID: "flat1", SourceURL: "https://youtube.test/watch?v=flat1"},
	}, nil
}

func newTestScanner(fetch domain.Downloader, channels domain.ChannelSource, lib domain.Library) *Scanner {
	return NewScanner(stubTopics{}, fetch, channels, lib,
		slog.New(slog.NewTextHandler(io.Discard, nil)), time.Hour)
}

// Browse is the only listing that carries view counts and upload dates, so it
// has to be the one that is used when it works.
func TestScanPrefersBrowseListing(t *testing.T) {
	published := time.Now().AddDate(0, 0, -3)
	channels := &stubChannels{uploads: domain.ChannelUploads{
		Videos: []domain.ExternalVideo{{
			ID:          "browse1",
			SourceURL:   "https://youtube.test/watch?v=browse1",
			ViewCount:   1_500_000,
			PublishedAt: published,
		}},
	}}
	downloader := &countingDownloader{}
	library := &recordingLibrary{known: map[string]bool{}, topics: map[string][]string{}}

	scanner := newTestScanner(downloader, channels, library)
	videos, err := scanner.listSource(context.Background(), "https://www.youtube.com/@a/videos", 30)
	if err != nil {
		t.Fatalf("listSource: %v", err)
	}

	if len(videos) != 1 || videos[0].ID != "browse1" {
		t.Fatalf("videos = %v, want the browse result", videos)
	}
	if videos[0].ViewCount != 1_500_000 || videos[0].PublishedAt.IsZero() {
		t.Fatalf("browse listing must carry views and a date, got %+v", videos[0])
	}
	if downloader.listCalls != 0 {
		t.Errorf("flat listing called %d times while browse worked, want 0", downloader.listCalls)
	}
}

// The whole reason browse is optional: it is undocumented and may break, and a
// scan that loses view counts is still a working scan.
func TestScanFallsBackToFlatListingWhenBrowseFails(t *testing.T) {
	channels := &stubChannels{err: errors.New("innertube changed shape")}
	downloader := &countingDownloader{}
	library := &recordingLibrary{known: map[string]bool{}, topics: map[string][]string{}}

	scanner := newTestScanner(downloader, channels, library)
	videos, err := scanner.listSource(context.Background(), "https://www.youtube.com/@a/videos", 30)
	if err != nil {
		t.Fatalf("a browse failure must not fail the scan: %v", err)
	}

	if len(videos) != 1 || videos[0].ID != "flat1" {
		t.Fatalf("videos = %v, want the flat listing result", videos)
	}
	if downloader.listCalls != 1 {
		t.Errorf("flat listing called %d times, want 1", downloader.listCalls)
	}
}

// Browse addresses channels. A playlist is not one, so it must not be sent
// there at all.
func TestPlaylistSourcesSkipBrowse(t *testing.T) {
	channels := &stubChannels{}
	downloader := &countingDownloader{}
	library := &recordingLibrary{known: map[string]bool{}, topics: map[string][]string{}}

	scanner := newTestScanner(downloader, channels, library)
	if _, err := scanner.listSource(context.Background(),
		"https://www.youtube.com/playlist?list=PL123", 30); err != nil {
		t.Fatalf("listSource: %v", err)
	}

	if len(channels.resolved) != 0 {
		t.Errorf("browse was consulted for a playlist: %v", channels.resolved)
	}
	if downloader.listCalls != 1 {
		t.Errorf("flat listing called %d times, want 1", downloader.listCalls)
	}
}

// The browse listing returns videos without saying whose channel they are, so
// the scanner has to stamp the owner on. Getting this wrong is silent: the
// catalog rejects a channel-less row, the scan still reports success, and the
// videos simply never appear — 3262 of them, in the run that found this.
func TestBrowseVideosAreAttributedToTheChannelOwner(t *testing.T) {
	channels := &stubChannels{uploads: domain.ChannelUploads{
		Videos: []domain.ExternalVideo{{
			ID:        "browse1",
			SourceURL: "https://youtube.test/watch?v=browse1",
		}},
	}}
	library := &recordingLibrary{known: map[string]bool{}, channels: map[string]domain.ExternalVideo{}}
	scanner := newTestScanner(&countingDownloader{}, channels, library)

	owner := domain.ChannelMetadata{ID: "UC123", Name: "Marques Brownlee", Handle: "@mkbhd"}
	_, added, err := scanner.scanSource(context.Background(), "",
		"https://www.youtube.com/@mkbhd/videos", owner, 30)
	if err != nil {
		t.Fatalf("scanSource: %v", err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1 — a video with no channel is dropped by the catalog", added)
	}

	got := library.channels["browse1"]
	if got.ChannelID != "UC123" || got.ChannelName != "Marques Brownlee" {
		t.Fatalf("channel = %q/%q, want UC123/Marques Brownlee", got.ChannelID, got.ChannelName)
	}
	if got.ChannelID == "src:https://www.youtube.com/@mkbhd/videos" {
		t.Fatal("video was attributed to a synthetic channel invented from the source URL")
	}
}

// With no owner and no channel on the video there is nothing truthful to write,
// so the video is skipped rather than filed under a made-up channel.
func TestVideoWithNoKnownChannelIsSkipped(t *testing.T) {
	channels := &stubChannels{uploads: domain.ChannelUploads{
		Videos: []domain.ExternalVideo{{
			ID:        "orphan",
			SourceURL: "https://youtube.test/watch?v=orphan",
		}},
	}}
	library := &recordingLibrary{known: map[string]bool{}, channels: map[string]domain.ExternalVideo{}}
	scanner := newTestScanner(&countingDownloader{}, channels, library)

	_, added, err := scanner.scanSource(context.Background(), "",
		"https://www.youtube.com/@unknown/videos", domain.ChannelMetadata{}, 30)
	if err != nil {
		t.Fatalf("scanSource: %v", err)
	}
	if added != 0 {
		t.Fatalf("added = %d, want 0", added)
	}
}

func TestChannelRefFromURL(t *testing.T) {
	cases := map[string]string{
		"https://www.youtube.com/@mkbhd/videos":                           "@mkbhd",
		"https://www.youtube.com/@mkbhd":                                  "@mkbhd",
		"https://www.youtube.com/channel/UCyQobySFx_h9oFwsBV0KGdg/videos": "UCyQobySFx_h9oFwsBV0KGdg",
		"@handle": "@handle",
	}
	for input, want := range cases {
		if got := channelRefFromURL(input); got != want {
			t.Errorf("channelRefFromURL(%q) = %q, want %q", input, got, want)
		}
	}
}
