package usecase

import (
	"context"
	"log/slog"
	"strings"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// How many videos one expansion pass aims to add. Small enough that a pass is
// quick, large enough to stay ahead of someone scrolling.
const expandTarget = 40

// The most one expansion pass may take from any single channel.
//
// Expansion is allowed to widen the library and not to adopt a channel. Without
// a bound, a pass that finds a rich seam takes forty videos from one place, and
// the next pass takes the next forty — measured here as one uninvited channel
// holding 60 videos, another 31, another 25.
const maxExpandPerChannel = 3

// Expander brings new material into the library when the feed runs low.
//
// Two layers, tried in order of decreasing trust:
//
//  1. Deeper into the sources in topics.yaml. These are channels the user chose;
//     a channel with a thousand uploads has been read forty deep. Nothing here
//     can fail in a way that surprises anyone.
//  2. Related videos from InnerTube, seeded by videos already in the library.
//     Genuinely new channels, at the cost of an undocumented API — so its
//     failure is logged and stepped over, never returned.
//
// The ordering is the design. If layer 2 breaks permanently, the feed still
// refills from layer 1; it is only less varied.
//
// There used to be a third: upstream search on the topic name, described here
// as a last resort because search results are the least curated material
// available. It was, and it was also the thing quietly rewriting what the
// library is. topics.yaml opens by calling itself "the only content source in
// the system... it keeps the library something you curate rather than something
// you accumulate by browsing" — and by the time this was measured the library
// held 708 channels against 87 subscribed and 6 curated sources, with a third of
// Home coming from channels nobody had asked for.
//
// The mechanism was ordinary and hard to see. Expansion fires with the topic
// chip the viewer is looking at, so picking a thinly-stocked topic sent its
// *name* to YouTube search and stored whatever came back. Nothing filtered the
// results, and `store` skips videos already present — so asking again brought
// back the same channels and imported the next forty of their uploads. One
// channel reached 60 videos that way, another 31, another 25, none of them
// subscribed and none in any language this household watches.
//
// Related is kept because it is anchored: it starts from videos the library
// already has, so its worst case is a neighbour of something chosen rather than
// an arbitrary search result.
type Expander struct {
	downloader domain.Downloader
	related    domain.RelatedSource
	library    domain.Library
	topics     domain.TopicSource
	cursors    domain.CursorStore
	logger     *slog.Logger
}

func NewExpander(
	downloader domain.Downloader,
	related domain.RelatedSource,
	library domain.Library,
	topics domain.TopicSource,
	cursors domain.CursorStore,
	logger *slog.Logger,
) *Expander {
	return &Expander{
		downloader: downloader,
		related:    related,
		library:    library,
		topics:     topics,
		cursors:    cursors,
		logger:     logger,
	}
}

func (e *Expander) Expand(ctx context.Context, topic string, seedVideoIDs []string) (int, error) {
	added := 0

	added += e.deepen(ctx, topic)
	if added >= expandTarget {
		return added, nil
	}

	added += e.fromRelated(ctx, seedVideoIDs)
	return added, nil
}

// deepen reads further into the curated sources for this topic, resuming from
// the stored cursor so each pass sees material the last one did not.
func (e *Expander) deepen(ctx context.Context, topic string) int {
	config, err := e.topics.Load(ctx)
	if err != nil {
		e.logger.Warn("load topics", "error", err)
		return 0
	}

	added := 0
	for _, t := range config.Topics {
		if topic != "" && !strings.EqualFold(t.Name, topic) {
			continue
		}
		for _, source := range t.Sources {
			offset, err := e.cursors.NextOffset(ctx, source)
			if err != nil {
				e.logger.Warn("read source cursor", "source", source, "error", err)
				continue
			}

			_, videos, err := e.downloader.ListPlaylist(ctx, source, offset, config.PerSourceLimit)
			if err != nil {
				e.logger.Warn("deepen source", "source", source, "error", err)
				continue
			}
			if len(videos) == 0 {
				continue // source exhausted
			}

			for i := range videos {
				videos[i].Topics = []string{t.Name}
			}
			added += e.store(ctx, videos, viaSource)

			if err := e.cursors.AdvanceOffset(ctx, source, int32(len(videos))); err != nil {
				e.logger.Warn("advance source cursor", "source", source, "error", err)
			}
			if added >= expandTarget {
				return added
			}
		}
	}
	return added
}

// fromRelated asks YouTube what sits beside videos the viewer has watched.
// Failure is expected occasionally and is never returned upward.
func (e *Expander) fromRelated(ctx context.Context, seedVideoIDs []string) int {
	added := 0
	for _, seed := range seedVideoIDs {
		videos, err := e.related.Related(ctx, seed)
		if err != nil {
			e.logger.Warn("related lookup", "seed", seed, "error", err)
			continue
		}
		added += e.store(ctx, videos, viaRelated)
		if added >= expandTarget {
			return added
		}
	}
	return added
}

// store writes metadata only. Nothing is downloaded here: a video becomes a row
// the feed can rank, and bytes are fetched later, if and when someone presses
// play. That is what keeps an expansion cheap enough to run mid-scroll.
//
// "ABSENT" matches the state the topic scanner already uses for a metadata-only
// row: known to the catalog, never on disk. It is deliberately not a word about
// downloading — nothing here queues one.
//
// Topics are whatever the caller already set: deepen files videos under the
// curated source's topic, related and search leave them unfiled. YouTube's own
// category arrives later, free, the first time full metadata is fetched for the
// video — see CLAUDE.md §7.
// Where a video was reached from. Recorded so the question "who asked for this"
// can be answered from the data rather than guessed at — see
// migrations/0012_discovered_via.sql for the guess that was wrong.
const (
	viaSource  = "SOURCE"
	viaRelated = "RELATED"
)

func (e *Expander) store(ctx context.Context, videos []domain.ExternalVideo, via string) int {
	added := 0
	// How many this pass has taken from each channel.
	//
	// A pass may broaden the library; it may not adopt a channel. Forty videos
	// from one place is not expansion, it is a subscription nobody pressed —
	// and repeated over enough passes it is how a channel reaches sixty videos
	// here without ever being chosen. Three leaves room for a genuinely apt
	// neighbour without letting one result become a section of the library.
	perChannel := map[string]int{}
	for _, v := range videos {
		if v.ID == "" || v.SourceURL == "" {
			continue
		}
		if v.ChannelID != "" {
			if perChannel[v.ChannelID] >= maxExpandPerChannel {
				continue
			}
		}
		if _, found, err := e.library.FindBySourceURL(ctx, v.SourceURL); err == nil && found {
			continue
		}

		if err := e.library.UpsertChannel(ctx, v); err != nil {
			e.logger.Warn("upsert channel", "video", v.ID, "error", err)
			continue
		}
		v.DiscoveredVia = via
		if err := e.library.UpsertVideo(ctx, v, "ABSENT"); err != nil {
			e.logger.Warn("upsert video", "video", v.ID, "error", err)
			continue
		}
		perChannel[v.ChannelID]++
		added++
	}
	return added
}
