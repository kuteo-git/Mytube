import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { trackURL, useTranslatedTrack } from './use-translated-track'

function Harness({
  vttVersion,
  complete,
  videoId = 'abc',
}: {
  vttVersion: number
  complete: boolean
  videoId?: string
}) {
  const revision = useTranslatedTrack(videoId, vttVersion, complete)
  return <span data-testid="rev">{revision}</span>
}

function renderHook(
  props: Parameters<typeof Harness>[0],
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } }),
) {
  const view = render(
    <QueryClientProvider client={client}>
      <Harness {...props} />
    </QueryClientProvider>,
  )
  const rev = () => Number(view.getByTestId('rev').textContent)
  const set = (next: Parameters<typeof Harness>[0]) =>
    view.rerender(
      <QueryClientProvider client={client}>
        <Harness {...next} />
      </QueryClientProvider>,
    )
  return { rev, set }
}

describe('the address given to the generated track', () => {
  it('is untouched before anything has been written', () => {
    expect(trackURL('/media/abc/x.vi-mt.vtt', true, 0)).toBe('/media/abc/x.vi-mt.vtt')
  })

  it('carries the revision once the file has been rewritten', () => {
    // The whole fix. The file keeps its name while it is rewritten after every
    // batch, and a browser will not fetch an address it already has — so the
    // copy on screen stayed the handful of lines that existed when it was first
    // asked for, and the subtitles stopped part way through a video the server
    // had finished translating.
    expect(trackURL('/media/abc/x.vi-mt.vtt', true, 2)).toBe(
      '/media/abc/x.vi-mt.vtt?v=2',
    )
  })

  it('leaves every other subtitle alone', () => {
    // Written once when the video was downloaded and never changed. A version
    // on those would defeat the browser's cache for nothing.
    expect(trackURL('/media/abc/x.en.vtt', false, 3)).toBe('/media/abc/x.en.vtt')
  })

  it('does not break an address that already has a query', () => {
    expect(trackURL('/s?lang=vi', true, 1)).toBe('/s?lang=vi&v=1')
  })
})

describe('when the track is fetched again', () => {
  it('not at all until something has been written', () => {
    const { rev } = renderHook({ vttVersion: 0, complete: false })
    expect(rev()).toBe(0)
  })

  it('once as soon as there is a file to read', () => {
    // So there is something to watch immediately, rather than the option
    // appearing and staying empty until the whole pass finishes.
    const { rev, set } = renderHook({ vttVersion: 0, complete: false })
    set({ vttVersion: 1, complete: false })
    expect(rev()).toBe(1)
  })

  it('not again for every batch in between', () => {
    // The file is rewritten every fifteen seconds or so. Refetching each time
    // would blink the subtitles for the length of the video.
    const { rev, set } = renderHook({ vttVersion: 0, complete: false })
    set({ vttVersion: 1, complete: false })
    set({ vttVersion: 2, complete: false })
    set({ vttVersion: 7, complete: false })
    expect(rev()).toBe(1)
  })

  it('once more when the translation is finished', () => {
    const { rev, set } = renderHook({ vttVersion: 0, complete: false })
    set({ vttVersion: 1, complete: false })
    set({ vttVersion: 8, complete: true })
    expect(rev()).toBe(2)
  })

  it('and never a third time', () => {
    const { rev, set } = renderHook({ vttVersion: 0, complete: false })
    set({ vttVersion: 1, complete: false })
    set({ vttVersion: 8, complete: true })
    set({ vttVersion: 9, complete: true })
    expect(rev()).toBe(2)
  })

  it('starts again from nothing on a different video', () => {
    const { rev, set } = renderHook({ vttVersion: 0, complete: false })
    set({ vttVersion: 1, complete: false })
    expect(rev()).toBe(1)

    set({ vttVersion: 0, complete: false, videoId: 'other' })
    expect(rev()).toBe(0)
  })
})

describe('the query that is asked again', () => {
  // The regression this exists for. The key is `['video', <profile>, <id>]` —
  // the profile sits in the middle — and this hook used to name
  // `['video', <id>]`, which matches no key at all. So the effect ran, the
  // revision moved, every test above passed, and the subtitle list was never
  // refetched: the translation finished and the VI option did not appear until
  // the page was reloaded.
  //
  // The revision alone cannot catch that, which is why it did not. This asserts
  // on the cache instead.
  it('is the one the video was actually stored under', () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const key = ['video', 'u_luc', 'abc']
    client.setQueryData(key, { id: 'abc', subtitles: [] })
    expect(client.getQueryState(key)?.isInvalidated).toBe(false)

    const { set } = renderHook({ vttVersion: 0, complete: false }, client)
    set({ vttVersion: 1, complete: false })

    expect(client.getQueryState(key)?.isInvalidated).toBe(true)
  })
})

describe('a second video opened without reloading the page', () => {
  // The reported fault: playing one video, pressing another in the rail. The
  // English track arrived and the machine translation never appeared.
  //
  // `vttVersion` counts every translated file this page has written, not this
  // video's — nothing resets it — so the second video opened on a number
  // already above zero. "The first write has happened" was true before its
  // pass had written a byte, the one invalidation that makes the Vietnamese
  // track appear was spent there, and the real first write found the hook
  // already used up.
  it('waits for a write of its own before asking for the list', () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const key = ['video', 'u_luc', 'second']
    const { rev, set } = renderHook({ vttVersion: 0, complete: false }, client)

    // The first video runs a translation to the end.
    set({ vttVersion: 1, complete: false })
    set({ vttVersion: 6, complete: true })

    // The viewer presses the next video. Its own pass has written nothing.
    set({ vttVersion: 6, complete: true, videoId: 'second' })
    client.setQueryData(key, { id: 'second', subtitles: [] })
    expect(rev()).toBe(0)
    expect(client.getQueryState(key)?.isInvalidated).toBe(false)

    // Its first batch lands.
    set({ vttVersion: 7, complete: false, videoId: 'second' })
    expect(rev()).toBe(1)
    expect(client.getQueryState(key)?.isInvalidated).toBe(true)
  })

  it('still announces the finish of the second video', () => {
    const { rev, set } = renderHook({ vttVersion: 4, complete: true })
    set({ vttVersion: 4, complete: true, videoId: 'second' })
    set({ vttVersion: 5, complete: false, videoId: 'second' })
    set({ vttVersion: 9, complete: true, videoId: 'second' })
    expect(rev()).toBe(2)
  })
})
