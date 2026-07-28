package innertube

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const sampleResponse = `{
  "contents": {
    "twoColumnWatchNextResults": {
      "secondaryResults": {
        "secondaryResults": {
          "results": [
            {
              "compactVideoRenderer": {
                "videoId": "abc123",
                "title": { "simpleText": "A related video" },
                "longBylineText": { "runs": [ { "text": "Some Channel" } ] },
                "lengthText": { "simpleText": "12:34" },
                "thumbnail": { "thumbnails": [ { "url": "https://i.ytimg.com/vi/abc123/hq.jpg" } ] }
              }
            },
            { "continuationItemRenderer": { "trigger": "unused" } }
          ]
        }
      }
    }
  }
}`

func TestRelatedParsesCompactVideoRenderers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleResponse))
	}))
	defer server.Close()

	client := New(server.Client())
	client.endpoint = server.URL

	videos, err := client.Related(t.Context(), "seed1")
	if err != nil {
		t.Fatalf("Related: %v", err)
	}
	if len(videos) != 1 {
		t.Fatalf("got %d videos, want 1 (non-video renderers must be skipped)", len(videos))
	}

	v := videos[0]
	if v.ID != "abc123" {
		t.Errorf("ID = %q, want abc123", v.ID)
	}
	if v.Title != "A related video" {
		t.Errorf("Title = %q", v.Title)
	}
	if v.ChannelName != "Some Channel" {
		t.Errorf("ChannelName = %q", v.ChannelName)
	}
	if v.DurationSeconds != 754 {
		t.Errorf("DurationSeconds = %d, want 754", v.DurationSeconds)
	}
	if v.SourceURL != "https://www.youtube.com/watch?v=abc123" {
		t.Errorf("SourceURL = %q", v.SourceURL)
	}
	if len(v.Topics) != 0 {
		t.Errorf("related videos must not be assigned a topic, got %v", v.Topics)
	}
}

func TestRelatedReturnsNothingRatherThanFailingOnUnexpectedShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"contents":{}}`))
	}))
	defer server.Close()

	client := New(server.Client())
	client.endpoint = server.URL

	videos, err := client.Related(t.Context(), "seed1")
	if err != nil {
		t.Fatalf("a shape change must not be an error, got %v", err)
	}
	if len(videos) != 0 {
		t.Fatalf("got %d videos, want 0", len(videos))
	}
}

func TestParseDuration(t *testing.T) {
	cases := map[string]int32{
		"0:45":    45,
		"12:34":   754,
		"1:02:03": 3723,
		"":        0,
		"LIVE":    0,
	}
	for input, want := range cases {
		if got := parseDuration(input); got != want {
			t.Errorf("parseDuration(%q) = %d, want %d", input, got, want)
		}
	}
}
