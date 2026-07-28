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

// Expander brings new material into the library when the feed runs low.
//
// Three layers, tried in order of decreasing trust:
//
//  1. Deeper into the sources in topics.yaml. These are channels the user chose;
//     a channel with a thousand uploads has been read forty deep. Nothing here
//     can fail in a way that surprises anyone.
//  2. Related videos from InnerTube. Genuinely new channels, at the cost of an
//     undocumented API — so its failure is logged and stepped over, never
//     returned.
//  3. Upstream search on the topic name. The last resort, because search results
//     are the least curated material available.
//
// The ordering is the design. If layer 2 breaks permanently, the feed still
// refills from layer 1; it is only less varied.
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
	if added >= expandTarget {
		return added, nil
	}

	added += e.fromSearch(ctx, topic)
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

			added += e.store(ctx, videos, t.Name)

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
		added += e.store(ctx, videos, "")
		if added >= expandTarget {
			return added
		}
	}
	return added
}

func (e *Expander) fromSearch(ctx context.Context, topic string) int {
	if topic == "" {
		return 0
	}
	videos, err := e.downloader.Search(ctx, topic, expandTarget)
	if err != nil {
		e.logger.Warn("expand by search", "topic", topic, "error", err)
		return 0
	}
	return e.store(ctx, videos, "")
}

// store writes metadata only. Nothing is downloaded here: a video becomes a row
// the feed can rank, and bytes are fetched later, if and when someone presses
// play. That is what keeps an expansion cheap enough to run mid-scroll.
//
// "QUEUED" matches the state the topic scanner already uses for a metadata-only
// row: known to the catalog, not yet on disk.
//
// fallbackTopic is used only when the video is genuinely new and a category
// fetch fails or comes back empty. deepen passes the curated source's topic
// name; related and search pass "" since they have no such fallback.
func (e *Expander) store(ctx context.Context, videos []domain.ExternalVideo, fallbackTopic string) int {
	added := 0
	for _, v := range videos {
		if v.ID == "" || v.SourceURL == "" {
			continue
		}
		if _, found, err := e.library.FindBySourceURL(ctx, v.SourceURL); err == nil && found {
			continue
		}

		v.Topics = e.categoryTopic(ctx, v.SourceURL, fallbackTopic)

		if err := e.library.UpsertChannel(ctx, v); err != nil {
			e.logger.Warn("upsert channel", "video", v.ID, "error", err)
			continue
		}
		if err := e.library.UpsertVideo(ctx, v, "QUEUED"); err != nil {
			e.logger.Warn("upsert video", "video", v.ID, "error", err)
			continue
		}
		added++
	}
	return added
}

// categoryTopic fetches YouTube's own category for a video new to the
// library (CLAUDE.md §7) and uses it as the topic. Cost is paid only here,
// once per genuinely new video — store's caller has already confirmed the
// video was not previously known.
func (e *Expander) categoryTopic(ctx context.Context, sourceURL, fallbackTopic string) []string {
	full, err := e.downloader.Preview(ctx, sourceURL)
	if err != nil || full.Category == "" {
		if err != nil {
			e.logger.Warn("fetch category", "source", sourceURL, "error", err)
		}
		if fallbackTopic != "" {
			return []string{fallbackTopic}
		}
		return nil
	}
	return []string{full.Category}
}
