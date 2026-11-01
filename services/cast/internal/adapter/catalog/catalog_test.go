package catalog

import (
	"testing"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
)

func TestAVideoIsDescribedForTheQueue(t *testing.T) {
	got := toCatalogVideo(&catalogv1.Video{
		Id: "abc", Title: "Bài", DurationSeconds: 245,
		Channel:       &catalogv1.Channel{Name: "Kênh"},
		ThumbnailPath: "https://i.ytimg.com/vi/abc/hqdefault.jpg",
		MediaState:    catalogv1.MediaState_MEDIA_STATE_READY,
	})
	if got.Item.Title != "Bài" || got.Item.Channel != "Kênh" || got.Item.DurationS != 245 ||
		got.Item.ThumbnailURL != "https://i.ytimg.com/vi/abc/hqdefault.jpg" || got.Live || got.Unavailable {
		t.Fatalf("got %+v", got)
	}
}

func TestARelativeThumbnailIsUnderMedia(t *testing.T) {
	if got := toCatalogVideo(&catalogv1.Video{ThumbnailPath: "abc/thumb.jpg"}); got.Item.ThumbnailURL != "/media/abc/thumb.jpg" {
		t.Fatalf("thumbnail = %q", got.Item.ThumbnailURL)
	}
}

func TestBroadcastsAndRefusedVideosAreMarked(t *testing.T) {
	for _, v := range []*catalogv1.Video{
		{LiveStatus: "is_live"}, {LiveStatus: "is_upcoming"}, {IsLiveNow: true},
	} {
		if !toCatalogVideo(v).Live {
			t.Errorf("%+v not marked live", v)
		}
	}
	if toCatalogVideo(&catalogv1.Video{LiveStatus: "was_live"}).Live {
		t.Error("a finished broadcast is a recording and can be cast")
	}
	if !toCatalogVideo(&catalogv1.Video{MediaState: catalogv1.MediaState_MEDIA_STATE_UNAVAILABLE}).Unavailable {
		t.Error("an unavailable video was not marked")
	}
}
