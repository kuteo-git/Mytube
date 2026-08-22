import type { Channel } from '../domain/video'
import { useSetSubscription } from '../application/queries'
import { Avatar } from '@/shared/ui/primitives'

import { hueFromId } from '@/shared/lib/hue'
import { mediaURL } from '@/shared/lib/media'
import { useFormat } from '@/shared/lib/useFormat'

/**
 * Channel identity, matching the reference layout in Example/channel.png.
 *
 * The tabs from that screenshot — Shorts, Releases, Playlists, and a Popular
 * row sorted by view count — are deliberately absent. Flat listings return no
 * view count and this library holds no Shorts or releases, so those controls
 * would be decoration over nothing.
 */
export function ChannelHeader({ channel, videoCount }: { channel: Channel; videoCount: number }) {
  const fmt = useFormat()
  const setSubscription = useSetSubscription(channel.id)

  return (
    <header>
      {channel.bannerPath ? (
        <img
          src={`/media/${channel.bannerPath}`}
          alt=""
          className="aspect-[6/1] w-full rounded-xl object-cover"
        />
      ) : (
        // Not every channel has a banner. An empty strip in the brand hue keeps
        // the page's proportions without inventing artwork.
        <div
          className="aspect-[6/1] w-full rounded-xl"
          style={{ background: `linear-gradient(120deg, hsl(${hueFromId(channel.id)} 40% 28%), #0f0f0f)` }}
        />
      )}

      <div className="mt-4 flex flex-wrap items-center gap-4">
        <Avatar
          hue={hueFromId(channel.id)}
          name={channel.name}
          src={mediaURL(channel.avatarPath)}
          size={96}
        />

        <div className="min-w-0">
          <h1 className="text-2xl font-medium">{channel.name}</h1>
          <p className="mt-1 text-sm text-text-2">
            {channel.handle && <span>{channel.handle} · </span>}
            {channel.subscriberCount > 0 && (
              <span>{fmt.subscribers(channel.subscriberCount)} · </span>
            )}
            {videoCount > 0 && <span>{videoCount} in your library</span>}
          </p>

          <button
            type="button"
            aria-pressed={channel.subscribed}
            disabled={setSubscription.isPending}
            onClick={() => setSubscription.mutate(!channel.subscribed)}
            className={
              'mt-3 rounded-full px-4 py-2 text-sm font-medium transition-colors duration-150 ease-out disabled:opacity-60 ' +
              (channel.subscribed
                ? 'bg-surface hover:bg-surface-hover'
                : 'bg-text text-bg hover:bg-text/90')
            }
          >
            {channel.subscribed ? 'Subscribed' : 'Subscribe'}
          </button>
        </div>
      </div>
    </header>
  )
}
