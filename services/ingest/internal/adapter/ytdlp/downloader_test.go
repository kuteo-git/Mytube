package ytdlp

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	goytdlp "github.com/lrstanley/go-ytdlp"
)

// TestFetchChannelFeedParsing verifies the XML parsing of a real RSS feed
// shape without making a network call. The XML is a snapshot of two entries
// from a live feed, trimmed to the fields the parser actually reads.
func TestFetchChannelFeedParsing(t *testing.T) {
	feedXML := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns:yt="http://www.youtube.com/xml/schemas/2015"
      xmlns:media="http://search.yahoo.com/mrss/"
      xmlns="http://www.w3.org/2005/Atom">
 <entry>
  <id>yt:video:ElhkWZdPDEc</id>
  <yt:videoId>ElhkWZdPDEc</yt:videoId>
  <yt:channelId>UCXuqSBlHAE6Xw-yeJA0Tunw</yt:channelId>
  <title>First Person to Say Hi Wins NEW Screwdriver</title>
  <published>2026-08-04T21:10:13+00:00</published>
  <media:group>
   <media:community>
    <media:statistics views="516195"/>
   </media:community>
  </media:group>
 </entry>
 <entry>
  <id>yt:video:1oIyk13JwQM</id>
  <yt:videoId>1oIyk13JwQM</yt:videoId>
  <yt:channelId>UCXuqSBlHAE6Xw-yeJA0Tunw</yt:channelId>
  <title>Does Linux use less RAM?</title>
  <published>2026-08-03T17:00:12+00:00</published>
  <media:group>
   <media:community>
    <media:statistics views="1850432"/>
   </media:community>
  </media:group>
 </entry>
</feed>`

	var feed rssFeed
	if err := xml.NewDecoder(strings.NewReader(feedXML)).Decode(&feed); err != nil {
		t.Fatalf("decode RSS XML: %v", err)
	}

	if len(feed.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(feed.Entries))
	}

	// First entry.
	e1 := feed.Entries[0]
	if e1.VideoID != "ElhkWZdPDEc" {
		t.Errorf("entry[0].VideoID = %q, want ElhkWZdPDEc", e1.VideoID)
	}
	if e1.Published != "2026-08-04T21:10:13+00:00" {
		t.Errorf("entry[0].Published = %q", e1.Published)
	}
	if e1.Media.Community.Statistics.Views != "516195" {
		t.Errorf("entry[0].Views = %q, want 516195", e1.Media.Community.Statistics.Views)
	}

	// Second entry.
	e2 := feed.Entries[1]
	if e2.VideoID != "1oIyk13JwQM" {
		t.Errorf("entry[1].VideoID = %q, want 1oIyk13JwQM", e2.VideoID)
	}
	if e2.Media.Community.Statistics.Views != "1850432" {
		t.Errorf("entry[1].Views = %q, want 1850432", e2.Media.Community.Statistics.Views)
	}
}

// TestFetchChannelFeedParsingEmptyFeed verifies that an RSS feed with no entries
// parses cleanly and returns an empty result rather than an error.
func TestFetchChannelFeedParsingEmptyFeed(t *testing.T) {
	feedXML := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns:yt="http://www.youtube.com/xml/schemas/2015"
      xmlns:media="http://search.yahoo.com/mrss/"
      xmlns="http://www.w3.org/2005/Atom">
</feed>`

	var feed rssFeed
	if err := xml.NewDecoder(strings.NewReader(feedXML)).Decode(&feed); err != nil {
		t.Fatalf("decode empty RSS XML: %v", err)
	}

	if len(feed.Entries) != 0 {
		t.Fatalf("got %d entries, want 0", len(feed.Entries))
	}
}

// TestFetchChannelFeedParsingMissingFields verifies that entries with missing
// optional fields (no views, no published date) parse without error.
func TestFetchChannelFeedParsingMissingFields(t *testing.T) {
	feedXML := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns:yt="http://www.youtube.com/xml/schemas/2015"
      xmlns:media="http://search.yahoo.com/mrss/"
      xmlns="http://www.w3.org/2005/Atom">
 <entry>
  <id>yt:video:abc123def01</id>
  <yt:videoId>abc123def01</yt:videoId>
  <title>Video with no stats</title>
 </entry>
</feed>`

	var feed rssFeed
	if err := xml.NewDecoder(strings.NewReader(feedXML)).Decode(&feed); err != nil {
		t.Fatalf("decode RSS XML with missing fields: %v", err)
	}

	if len(feed.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(feed.Entries))
	}
	if feed.Entries[0].VideoID != "abc123def01" {
		t.Errorf("VideoID = %q", feed.Entries[0].VideoID)
	}
	// These should be their zero values.
	if feed.Entries[0].Published != "" {
		t.Errorf("Published = %q, want empty", feed.Entries[0].Published)
	}
	if feed.Entries[0].Media.Community.Statistics.Views != "" {
		t.Errorf("Views = %q, want empty", feed.Entries[0].Media.Community.Statistics.Views)
	}
}

// TestFetchChannelFeedRealChannel verifies that a real RSS feed returns entries
// with the expected shape. Skipped in short mode because it reaches the network.
func TestFetchChannelFeedRealChannel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	// Linus Tech Tips — a large, active channel that will always have recent
	// uploads in its feed.
	dl := &Downloader{mediaRoot: "/tmp"}
	entries, err := dl.FetchChannelFeed(t.Context(), "UCXuqSBlHAE6Xw-yeJA0Tunw")
	if err != nil {
		t.Fatalf("FetchChannelFeed: %v", err)
	}

	if len(entries) == 0 {
		t.Fatal("feed returned 0 entries for an active channel")
	}
	if len(entries) > 15 {
		t.Errorf("feed returned %d entries, want at most 15", len(entries))
	}

	for _, e := range entries {
		if e.VideoID == "" {
			t.Errorf("entry has empty VideoID")
		}
		if len(e.VideoID) != 11 {
			t.Errorf("entry %q: VideoID length = %d, want 11", e.VideoID, len(e.VideoID))
		}
		if e.PublishedAt.IsZero() {
			t.Errorf("entry %q: PublishedAt is zero", e.VideoID)
		}
		// View count may be zero for brand-new videos, but most should have one.
		if e.ViewCount == 0 {
			t.Logf("entry %q: ViewCount is 0 (may be brand new)", e.VideoID)
		}
	}
}

// TestFetchChannelFeedNonExistentChannel verifies that a non-existent channel
// returns an error (HTTP 404).
func TestFetchChannelFeedNonExistentChannel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	dl := &Downloader{mediaRoot: "/tmp"}
	_, err := dl.FetchChannelFeed(t.Context(), "UCnonexistent123")
	if err == nil {
		t.Fatal("expected error for non-existent channel, got nil")
	}
}

// TestFetchChannelFeedTimeParsing verifies the RFC3339 timestamp parsing with
// various timezone offsets.
func TestFetchChannelFeedTimeParsing(t *testing.T) {
	inputs := []struct {
		raw  string
		want time.Time
	}{
		{"2026-08-04T21:10:13+00:00", time.Date(2026, 8, 4, 21, 10, 13, 0, time.UTC)},
		{"2026-08-03T17:00:12+00:00", time.Date(2026, 8, 3, 17, 0, 12, 0, time.UTC)},
		{"2026-01-15T09:30:00+07:00", time.Date(2026, 1, 15, 2, 30, 0, 0, time.UTC)},
	}

	for _, tc := range inputs {
		parsed, err := time.Parse(time.RFC3339, tc.raw)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.raw, err)
			continue
		}
		if !parsed.Equal(tc.want) {
			t.Errorf("Parse(%q) = %v, want %v", tc.raw, parsed, tc.want)
		}
	}
}

func sptr(s string) *string { return &s }

func TestToExternalExtractsLanguageFromYtdlp(t *testing.T) {
	lang := "vi"
	info := &goytdlp.ExtractedInfo{
		ExtractedFormat: &goytdlp.ExtractedFormat{
			Language: &lang,
		},
		ID:    "test123",
		Title: sptr("Xin chao Viet Nam"),
	}
	v := toExternal(info)
	if v.Language != "vi" {
		t.Fatalf("Language = %q, want %q", v.Language, "vi")
	}
}

func TestToExternalLeavesLanguageEmptyWhenNotPresent(t *testing.T) {
	info := &goytdlp.ExtractedInfo{
		ID:    "test456",
		Title: sptr("BBC News"),
	}
	v := toExternal(info)
	if v.Language != "" {
		t.Fatalf("Language = %q, want empty when yt-dlp returns none", v.Language)
	}
}

func TestToExternalLanguageIsEmptyWhenNull(t *testing.T) {
	info := &goytdlp.ExtractedInfo{
		ID:    "test789",
		Title: sptr("Some Video"),
		ExtractedFormat: &goytdlp.ExtractedFormat{
			Language: sptr(""),
		},
	}
	v := toExternal(info)
	if v.Language != "" {
		t.Fatalf("Language = %q, want empty when yt-dlp returns empty string", v.Language)
	}
}
