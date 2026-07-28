package innertube

import (
	"encoding/json"
	"testing"
)

// A lockup as YouTube actually sends it: the title sits at a known path, but
// the item also embeds a context menu whose entries have "content" fields of
// their own.
const sampleLockup = `{
  "contentId": "abc123",
  "contentType": "LOCKUP_CONTENT_TYPE_VIDEO",
  "contentImage": {
    "thumbnailViewModel": {
      "image": { "sources": [
        { "url": "https://i.ytimg.com/vi/abc123/small.jpg", "width": 168, "height": 94 },
        { "url": "https://i.ytimg.com/vi/abc123/large.jpg", "width": 336, "height": 188 }
      ]},
      "overlays": [
        { "thumbnailBottomOverlayViewModel": {
            "badges": [ { "thumbnailBadgeViewModel": { "text": "12:49" } } ]
        }}
      ]
    }
  },
  "metadata": {
    "lockupMetadataViewModel": {
      "title": { "content": "Retro Tech: Game Boy" },
      "metadata": { "contentMetadataViewModel": { "metadataRows": [
        { "metadataParts": [
            { "text": { "content": "40M views" } },
            { "text": { "content": "7 years ago" } }
        ]}
      ]}},
      "menuButton": { "buttonViewModel": { "onTap": { "listItems": [
        { "listItemViewModel": { "title": { "content": "Add to queue" } } },
        { "listItemViewModel": { "title": { "content": "Save to Watch later" } } }
      ]}}}
    }
  }
}`

// The regression: the title was read by searching for any "content" key, which
// picked up a context-menu entry instead. Go randomises map iteration, so this
// surfaced as some cards being titled "Add to queue" — and only sometimes.
// Repeated because a single pass can pass by luck.
func TestLockupTitleIsNotAContextMenuEntry(t *testing.T) {
	var lockup map[string]any
	if err := json.Unmarshal([]byte(sampleLockup), &lockup); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	for i := 0; i < 50; i++ {
		video, ok := parseLockup(lockup)
		if !ok {
			t.Fatal("lockup was not parsed as a video")
		}
		if video.Title != "Retro Tech: Game Boy" {
			t.Fatalf("run %d: title = %q, want %q", i, video.Title, "Retro Tech: Game Boy")
		}
	}
}

func TestLockupCarriesViewsDateDurationAndLargestThumbnail(t *testing.T) {
	var lockup map[string]any
	if err := json.Unmarshal([]byte(sampleLockup), &lockup); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	video, ok := parseLockup(lockup)
	if !ok {
		t.Fatal("lockup was not parsed as a video")
	}

	if video.ID != "abc123" {
		t.Errorf("ID = %q", video.ID)
	}
	if video.ViewCount != 40_000_000 {
		t.Errorf("ViewCount = %d, want 40000000", video.ViewCount)
	}
	if video.PublishedAt.IsZero() {
		t.Error("PublishedAt is zero; the row carried a date")
	}
	if video.DurationSeconds != 769 {
		t.Errorf("DurationSeconds = %d, want 769", video.DurationSeconds)
	}
	if video.ThumbnailURL != "https://i.ytimg.com/vi/abc123/large.jpg" {
		t.Errorf("ThumbnailURL = %q, want the widest source", video.ThumbnailURL)
	}
	if video.SourceURL != "https://www.youtube.com/watch?v=abc123" {
		t.Errorf("SourceURL = %q", video.SourceURL)
	}
}

// Playlists, shorts and ad slots ride in the same list and must be skipped.
func TestNonVideoLockupIsSkipped(t *testing.T) {
	lockup := map[string]any{
		"contentId":   "PL123",
		"contentType": "LOCKUP_CONTENT_TYPE_PLAYLIST",
	}
	if _, ok := parseLockup(lockup); ok {
		t.Fatal("a playlist lockup was accepted as a video")
	}
}
