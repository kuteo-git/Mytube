import { Info, LayoutGrid, RotateCcw } from 'lucide-react'
import { useEffect, useState } from 'react'
import {
  useBucketSizes,
  useFeedMix,
  useSaveFeedMix,
} from '@/features/settings/application/queries'
import {
  adjustablePercent,
  FALLBACK_FIXED_SHARES,
  FEED_WINDOW,
  type FeedMix,
  type FeedMixKey,
  type FixedShares,
  setShare,
  videosPerWindow,
} from '@/features/settings/domain/feed-mix'
import { ActionBar } from '@/features/settings/ui/ActionBar'
import { SettingsSection } from '@/features/settings/ui/SettingsSection'
import { SliderRow } from '@/features/settings/ui/SliderRow'

/**
 * What the home feed is made of.
 *
 * The three sliders are the three sources of new material, and they always add
 * to a hundred: this is a division of one page, not three independent dials, so
 * moving one has to take from the others. The other two absorb it in proportion
 * to where they already were — see setShare.
 *
 * Saved explicitly rather than on every drag. The narration sliders write
 * straight through because you hear the result while dragging; this one is
 * bought by re-ranking the feed and losing your scroll position, which is not
 * something to do sixty times on the way to a number.
 */
export function FeedMixSettings({ headless = false }: { headless?: boolean } = {}) {
  const { data: stored, isError, isPending, refetch } = useFeedMix()
  const save = useSaveFeedMix()
  // Loaded separately and allowed to fail: it costs a full ranking pass, and the
  // sliders are usable without it.
  const { data: buckets } = useBucketSizes()
  const [mix, setMix] = useState<FeedMix | null>(null)

  // The server's copy is the starting point, and only until the first drag —
  // after that the local one is the truth or the slider would fight the hand
  // holding it.
  useEffect(() => {
    if (stored && !mix) {
      setMix({
        subscribedPercent: stored.subscribedPercent,
        affinityPercent: stored.affinityPercent,
        discoveryPercent: stored.discoveryPercent,
      })
    }
  }, [stored, mix])

  // A request that failed is not a request still running.
  //
  // Both used to render as "Loading…", so a gateway that was up but did not
  // know this endpoint — an older binary still running, which is exactly what
  // happened — left the section loading for ever with nothing to press and
  // nothing to read.
  if (isError) {
    return (
      <SettingsSection
        headless={headless}
        icon={<LayoutGrid size={18} />}
        title="Home feed"
        description="Could not read the current mix. The gateway may be running an older build that does not have this setting yet."
      >
        <button
          type="button"
          onClick={() => void refetch()}
          className="h-11 w-fit rounded-lg bg-surface-hover px-4 text-sm font-medium transition-opacity duration-150 ease-out hover:opacity-90"
        >
          Try again
        </button>
      </SettingsSection>
    )
  }

  if (isPending || !stored || !mix) {
    return (
      <SettingsSection
        icon={<LayoutGrid size={18} />}
        title="Home feed"
        description="Loading…"
      >
        <div className="h-24 animate-pulse rounded-lg bg-surface-input" />
      </SettingsSection>
    )
  }

  const defaults = stored.defaults
  // An older gateway does not send these. Falling back to the built-in figures
  // is better than rendering nothing, and they are right in every case except
  // one where somebody has moved the fresh-subscribed share.
  const fixed: FixedShares = stored.fixedShares ?? FALLBACK_FIXED_SHARES
  const saved: FeedMix = {
    subscribedPercent: stored.subscribedPercent,
    affinityPercent: stored.affinityPercent,
    discoveryPercent: stored.discoveryPercent,
  }
  const dirty = !sameMix(mix, saved)
  const isDefault = sameMix(mix, defaults)
  const drag = (key: FeedMixKey) => (value: number) =>
    setMix((current) => (current ? setShare(current, key, value) : current))

  return (
    <SettingsSection
      icon={<LayoutGrid size={18} />}
      title="Home feed"
      description="Where the new videos on your home page come from. The three add up to one page, so raising one lowers the others."
    >
      <FeedMixSlider
        label="Channels you follow"
        value={mix.subscribedPercent}
        onChange={drag('subscribedPercent')}
        fixed={fixed}
        available={buckets?.subscribed}
      />
      <FeedMixSlider
        label="More of what you watch"
        value={mix.affinityPercent}
        onChange={drag('affinityPercent')}
        hint="Channels you have not subscribed to, on subjects you keep coming back to."
        fixed={fixed}
        available={buckets?.affinity}
      />
      <FeedMixSlider
        label="Something new"
        value={mix.discoveryPercent}
        onChange={drag('discoveryPercent')}
        hint="Outside your usual subjects. Set this to zero and none will appear."
        fixed={fixed}
        available={buckets?.discovery}
      />

      <DefaultsNote defaults={defaults} fixed={fixed} />

      <ActionBar>
        <button
          type="button"
          disabled={!dirty || save.isPending}
          onClick={() => save.mutate(mix)}
          className="h-11 rounded-lg bg-invert-bg px-5 text-sm font-medium text-invert-text transition-opacity duration-150 ease-out hover:opacity-90 disabled:opacity-50"
        >
          {save.isPending ? 'Saving…' : 'Save'}
        </button>
        <button
          type="button"
          disabled={isDefault || save.isPending}
          onClick={() => setMix(defaults)}
          className="flex h-11 items-center gap-1.5 rounded-lg px-3 text-sm text-text-2 transition-colors duration-150 ease-out hover:bg-surface-hover disabled:opacity-50"
        >
          <RotateCcw size={14} />
          Reset to default
        </button>
        {/* Confirmation, not decoration: without it a save that changed nothing
            visible on this page is indistinguishable from a save that failed. */}
        {!dirty && save.isSuccess && (
          <span className="text-sm text-text-2">Saved — your feed has been rebuilt.</span>
        )}
        {save.isError && (
          <span className="text-sm text-brand">Could not save. Is the gateway running?</span>
        )}
      </ActionBar>
    </SettingsSection>
  )
}

function FeedMixSlider({
  label,
  value,
  onChange,
  hint,
  fixed,
  available,
}: {
  label: string
  value: number
  onChange: (v: number) => void
  hint?: string
  fixed: FixedShares
  /** How many videos this share has to choose from, once the count arrives. */
  available?: number
}) {
  const wanted = videosPerWindow(value, fixed)

  return (
    <SliderRow
      label={label}
      value={value}
      max={100}
      step={1}
      onChange={onChange}
      // Both, because neither alone is the answer. The percentage is what was
      // set; the count is what it means on the page you are about to look at.
      format={(v) => `${v}% · ${videosPerWindow(v, fixed)} of ${FEED_WINDOW}`}
      hint={[hint, supply(available, wanted)].filter(Boolean).join(' ')}
    />
  )
}

/**
 * What this share actually has to work with.
 *
 * The sliders divide a page, but a share can only be filled from videos that
 * exist. On this library the "more of what you watch" bucket held twenty-five
 * videos against three and a half thousand subscribed ones, so a 60% share spent
 * half of every page scraping that bucket's floor while far better videos went
 * unused — and there was nothing on this screen, or any other, that would have
 * shown it.
 *
 * Says so only when the share outruns the supply. A bucket with plenty in it is
 * not news, and a count printed under every slider would be nine words nobody
 * reads, which is how the one that matters gets missed.
 */
function supply(available: number | undefined, wantedPerWindow: number): string {
  if (available === undefined) return ''
  if (available === 0) {
    return 'Nothing in your library fits this right now, so its places go to the other two.'
  }
  // A share is only spread thin if a couple of pages would exhaust it.
  if (wantedPerWindow > 0 && available < wantedPerWindow * 3) {
    return `Only ${available} videos fit this, so a share this large repeats them or reaches well down the list.`
  }
  return `${available} videos fit this.`
}

/**
 * The defaults, spelled out.
 *
 * Two things are invisible without it: what the numbers were before anybody
 * touched them, and the fact that eighteen per cent of the page is never up for
 * division. A viewer who sets 25/60/15 and counts five subscribed videos on a
 * page of twenty-four should be able to find out why it was not six.
 */
function DefaultsNote({ defaults, fixed }: { defaults: FeedMix; fixed: FixedShares }) {
  const rows: Array<[string, number]> = [
    ['Channels you follow', defaults.subscribedPercent],
    ['More of what you watch', defaults.affinityPercent],
    ['Something new', defaults.discoveryPercent],
  ]

  return (
    <div className="rounded-lg bg-surface-input p-3 text-sm">
      <div className="flex items-center gap-2 font-medium">
        <Info size={15} className="text-text-2" />
        Defaults
      </div>
      <dl className="mt-2 space-y-1">
        {rows.map(([label, percent]) => (
          <div key={label} className="flex items-baseline justify-between gap-3">
            <dt className="text-text-2">{label}</dt>
            <dd className="tabular-nums">
              {percent}%{' '}
              <span className="text-text-2">
                · {videosPerWindow(percent, fixed)} of {FEED_WINDOW}
              </span>
            </dd>
          </div>
        ))}
      </dl>
      <p className="mt-2 leading-relaxed text-text-2">
        These three divide {adjustablePercent(fixed)}% of the page. The rest is kept
        for videos you are part way through ({fixed.continueWatching}%), ones you have
        finished and might want again ({fixed.rewatch}%), and new uploads from channels
        you follow ({fixed.freshSubscribed}%). None of those three is a taste — the
        first two are your watch history, and the last is how you find out a channel
        posted — so they are not divided here.
      </p>
    </div>
  )
}

function sameMix(a: FeedMix, b: FeedMix): boolean {
  return (
    a.subscribedPercent === b.subscribedPercent &&
    a.affinityPercent === b.affinityPercent &&
    a.discoveryPercent === b.discoveryPercent
  )
}
