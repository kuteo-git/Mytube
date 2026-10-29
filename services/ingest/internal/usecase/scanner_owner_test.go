package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// The playlist's owner is not the video's owner. Measured on the running
// library: three subscribed channels — Britney Spears, KARIK, Ngọc Phụng —
// carried the handle @kuluom7038, a household member whose playlists held
// their videos, and 25 more carried the handle of the account that imported
// them. The id came from the listing and the handle from the playlist's owner,
// so every scan of those subscriptions read somebody else's channel.
func TestPlaylistOwnersHandleIsNotStampedOnAnotherChannel(t *testing.T) {
	video := domain.ExternalVideo{ID: "v1", ChannelID: "UCgffc95YDBlkGrBAJUHUmXQ", ChannelName: "Britney Spears"}
	owner := domain.ChannelMetadata{ID: "UCwTzkYszmeRETnAlbTHrSRw", Name: "kuluom", Handle: "@kuluom7038"}

	applyOwner(&video, owner, true)

	if video.ChannelHandle != "" {
		t.Fatalf("handle = %q, want none: the playlist owner's handle on another channel", video.ChannelHandle)
	}
}

// Nor its name, when the listing named the channel by id alone.
func TestPlaylistOwnersNameIsNotStampedOnAnotherChannel(t *testing.T) {
	video := domain.ExternalVideo{ID: "v1", ChannelID: "UCgffc95YDBlkGrBAJUHUmXQ"}
	owner := domain.ChannelMetadata{ID: "UCwTzkYszmeRETnAlbTHrSRw", Name: "kuluom", Handle: "@kuluom7038"}

	applyOwner(&video, owner, true)

	if video.ChannelName == "kuluom" {
		t.Fatal("the playlist owner's name was written onto another channel")
	}
}

// A gap filled by the owner is filled whole: the id, the name and the handle
// all name the same channel.
func TestPlaylistOwnerFillsAMissingChannelWhole(t *testing.T) {
	video := domain.ExternalVideo{ID: "v1"}
	owner := domain.ChannelMetadata{ID: "UCowner", Name: "Owner", Handle: "@owner"}

	applyOwner(&video, owner, true)

	if video.ChannelID != "UCowner" || video.ChannelName != "Owner" || video.ChannelHandle != "@owner" {
		t.Fatalf("got %q/%q/%q", video.ChannelID, video.ChannelName, video.ChannelHandle)
	}
}

// A subscription is scanned by the id its row is keyed on. Handles change
// (@MicrosoftWindows now answers 404 for the channel it was stored on) and
// were written wrong (above); the id is the one thing that names the channel.
func TestSubscribedChannelIsScannedByItsID(t *testing.T) {
	got := channelVideosURL(domain.SubscribedChannel{ID: "UCVGOyzms_XJNk_DHqrffXCw", Handle: "@MicrosoftWindows"})
	if want := "https://www.youtube.com/channel/UCVGOyzms_XJNk_DHqrffXCw/videos"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// A row whose id is not a channel id still has its handle to go by.
	got = channelVideosURL(domain.SubscribedChannel{ID: "@legacy", Handle: "@legacy"})
	if want := "https://www.youtube.com/@legacy/videos"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// tabDownloader answers like yt-dlp for a channel with no Videos tab: the tab
// fails, the channel's uploads playlist lists what it published.
type tabDownloader struct {
	countingDownloader
	asked []string
}

func (d *tabDownloader) ListPlaylist(_ context.Context, url string, _, _ int32) (string, []domain.ExternalVideo, error) {
	d.asked = append(d.asked, url)
	if url == "https://www.youtube.com/playlist?list=UUwtrTCW2i7gbBHl9zqRA0cw" {
		return "Uploads", []domain.ExternalVideo{{ID: "short1", SourceURL: "https://youtube.test/watch?v=short1"}}, nil
	}
	return "", nil, errors.New(`playlist "` + url + `": exit code 1: exit status 1 ERROR: [youtube:tab] UCwtrTCW2i7gbBHl9zqRA0cw: This channel does not have a videos tab`)
}

// Measured: UCwtrTCW2i7gbBHl9zqRA0cw publishes Shorts and broadcasts and no
// Videos tab, so its /videos failed on every scan; its uploads playlist (UU…)
// lists all three kinds. Shorts are kept off Home by the Short probe, not by
// which tab is read.
func TestAChannelWithNoVideosTabIsReadFromItsUploads(t *testing.T) {
	downloader := &tabDownloader{}
	library := &recordingLibrary{known: map[string]bool{}, topics: map[string][]string{}}
	scanner := newTestScanner(downloader, nil, library)

	videos, err := scanner.listSource(context.Background(),
		"https://www.youtube.com/channel/UCwtrTCW2i7gbBHl9zqRA0cw/videos", 30)
	if err != nil {
		t.Fatalf("listSource: %v", err)
	}
	if len(videos) != 1 || videos[0].ID != "short1" {
		t.Fatalf("videos = %v, want the uploads playlist's", videos)
	}
}
