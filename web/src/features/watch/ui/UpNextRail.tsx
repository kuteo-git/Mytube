import { ChevronDown } from 'lucide-react'
import { useCallback, useState } from 'react'
import { Link } from 'react-router-dom'
import type { Video } from '@/features/catalog/domain/video'
import { useUpNext } from '@/features/catalog/application/queries'
import { InfiniteList } from '@/shared/ui/InfiniteList'
import { Pill, ThumbnailSurface } from '@/shared/ui/primitives'
import { formatDuration, formatRelative, formatViews } from '@/shared/lib/format'
import { hueFromId } from '@/shared/lib/hue'
import { mediaURL } from '@/shared/lib/media'

export function UpNextRail({
  current,
  exclude,
}: {
  current: Video
  /**
   * Videos already played this sitting. Filtered out rather than merely
   * skipped by "next", so the button and the top of this list always name the
   * same video — a rail whose first row is not what plays next is a lie.
   */
  exclude: ReadonlySet<string>
}) {
  const [channelFilter, setChannelFilter] = useState<string | undefined>(undefined)
  const [collapsed, setCollapsed] = useState(false)
  const { data, fetchNextPage, hasNextPage, isFetchingNextPage } = useUpNext(current.id, channelFilter)
  const handleLoadMore = useCallback(() => {
    void fetchNextPage()
  }, [fetchNextPage])
  const unseen = data?.filter((video) => !exclude.has(video.id))
  // Once everything on offer has been played this sitting, showing the list
  // again beats showing nothing: an empty rail reads as a broken page, and
  // "next" falls back the same way, so the two still agree about what plays.
  const videos = unseen && unseen.length > 0 ? unseen : data

  return (
    <aside className="flex w-full flex-col gap-3" aria-label="Up next">
      <div className="flex items-start gap-2 rounded-xl bg-surface px-3 py-2">
        <div className="min-w-0 flex-1">
          <p className="clamp-1 text-sm font-medium">
            Next: {videos?.[0]?.title ?? 'Nothing queued'}
          </p>
          <p className="clamp-1 text-xs text-text-2">{videos?.[0]?.channel.name}</p>
        </div>
        <button
          type="button"
          aria-label={collapsed ? 'Expand up next' : 'Collapse up next'}
          aria-expanded={!collapsed}
          onClick={() => setCollapsed((c) => !c)}
          className="grid h-8 w-8 shrink-0 place-items-center rounded-full hover:bg-surface-hover"
        >
          <ChevronDown
            size={20}
            className="transition-transform duration-150 ease-out"
            style={{ transform: collapsed ? 'rotate(-90deg)' : undefined }}
          />
        </button>
      </div>

      {!collapsed && (
        <>
          <div
            role="tablist"
            aria-label="Filter suggestions"
            className="flex gap-2 overflow-x-auto overscroll-x-contain no-scrollbar"
          >
            <Pill
              role="tab"
              aria-selected={channelFilter === undefined}
              active={channelFilter === undefined}
              onClick={() => setChannelFilter(undefined)}
              className="h-8 px-3 text-[13px]"
            >
              All
            </Pill>
            <Pill
              role="tab"
              aria-selected={channelFilter === current.channel.id}
              active={channelFilter === current.channel.id}
              onClick={() => setChannelFilter(current.channel.id)}
              className="h-8 px-3 text-[13px]"
            >
              From {current.channel.name}
            </Pill>
          </div>

          <ul className="flex flex-col gap-2">
            {videos?.map((video) => (
              <li key={video.id}>
                <SuggestionRow video={video} />
              </li>
            ))}
          </ul>

          <InfiniteList
            hasMore={Boolean(hasNextPage)}
            isLoading={isFetchingNextPage}
            onLoadMore={handleLoadMore}
          />
        </>
      )}
    </aside>
  )
}

function SuggestionRow({ video }: { video: Video }) {
  // "New" means recently ingested, not recently published — keep the window
  // tight so the badge stays meaningful instead of decorating every row.
  const isNew = Date.now() - new Date(video.addedAt).getTime() < 2 * 86_400_000

  return (
    <Link
      to={`/watch/${video.id}`}
      className="flex gap-2 rounded-xl p-1 transition-colors duration-150 ease-out hover:bg-surface-hover"
    >
      <div className="w-[168px] shrink-0">
        <ThumbnailSurface
          hue={hueFromId(video.id)}
          src={mediaURL(video.thumbnailPath)}
          alt={video.title}
          channelName={video.channel.name}
          rounded="rounded-lg"
        >
          <span className="absolute right-1 bottom-1 rounded bg-badge px-1 text-[11px] font-medium tabular-nums">
            {formatDuration(video.durationSeconds)}
          </span>
        </ThumbnailSurface>
      </div>
      <div className="min-w-0 flex-1">
        <h4 className="clamp-2 text-sm leading-5 font-medium">{video.title}</h4>
        <p className="mt-1 text-xs text-text-2">{video.channel.name}</p>
        <p className="text-xs text-text-2">
          {[
            video.viewCount > 0 ? formatViews(video.viewCount) : null,
            video.publishedAt ? formatRelative(video.publishedAt) : null,
          ]
            .filter(Boolean)
            .join(' • ')}
        </p>
        {isNew && (
          <span className="mt-1 inline-block rounded bg-surface px-1.5 py-0.5 text-[11px] text-text-2">
            New
          </span>
        )}
      </div>
    </Link>
  )
}
