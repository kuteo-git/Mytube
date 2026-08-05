import { afterEach, describe, expect, it, vi } from 'vitest'
import { shareURL, shareVideo } from './share-link'

afterEach(() => vi.unstubAllGlobals())

describe('the address a video is shared as', () => {
  it('is the one it was fetched from', () => {
    // Rather than one assembled from a guess about the id.
    expect(
      shareURL({ id: 'abc', sourceUrl: 'https://www.youtube.com/watch?v=abc' }),
    ).toBe('https://www.youtube.com/watch?v=abc')
  })

  it('is built from the id when nothing was recorded', () => {
    expect(shareURL({ id: 'abc' })).toBe('https://www.youtube.com/watch?v=abc')
    expect(shareURL({ id: 'abc', sourceUrl: '   ' })).toBe(
      'https://www.youtube.com/watch?v=abc',
    )
  })

  it('is never this library', () => {
    // The address used to be `window.location.href` — somewhere on the house
    // LAN, which is a link only to somebody sitting in the house and on the
    // same network, and a dead string anywhere else. This reverses CLAUDE.md
    // §5, which recorded Share as copying that address.
    expect(shareURL({ id: 'abc' })).not.toContain('localhost')
    expect(shareURL({ id: 'abc' })).toMatch(/^https:\/\/www\.youtube\.com\//)
  })

  it('refuses a recorded address that is not one', () => {
    // Ingest writes what it was given, and a half-written row should not
    // produce a link that goes nowhere.
    expect(shareURL({ id: 'abc', sourceUrl: 'abc' })).toBe(
      'https://www.youtube.com/watch?v=abc',
    )
  })
})

describe('what pressing Share does', () => {
  it('opens the sheet where the device has one', async () => {
    const share = vi.fn(async () => {})
    vi.stubGlobal('navigator', { share, clipboard: { writeText: vi.fn() } })

    const outcome = await shareVideo({ url: 'u', title: 't', canShare: true })

    expect(share).toHaveBeenCalledWith({ url: 'u', title: 't' })
    expect(outcome).toBe('shared')
  })

  it('copies where it does not', async () => {
    // A desktop mostly has no sheet, and a link on the clipboard is what people
    // do next anyway.
    const writeText = vi.fn(async () => {})
    vi.stubGlobal('navigator', { clipboard: { writeText } })

    const outcome = await shareVideo({ url: 'u', canShare: false })

    expect(writeText).toHaveBeenCalledWith('u')
    expect(outcome).toBe('copied')
  })

  it('copies where the browser has no sheet at all, whatever the pointer says', async () => {
    const writeText = vi.fn(async () => {})
    vi.stubGlobal('navigator', { clipboard: { writeText } })

    expect(await shareVideo({ url: 'u', canShare: true })).toBe('copied')
    expect(writeText).toHaveBeenCalled()
  })

  it('does not fall back to the clipboard when the sheet is dismissed', async () => {
    // Cancelling is an answer, not a failure to reach the device — and putting
    // something on the clipboard after somebody backed out is doing a thing
    // they just declined.
    const writeText = vi.fn(async () => {})
    vi.stubGlobal('navigator', {
      share: vi.fn(async () => {
        throw new Error('AbortError')
      }),
      clipboard: { writeText },
    })

    expect(await shareVideo({ url: 'u', canShare: true })).toBe('cancelled')
    expect(writeText).not.toHaveBeenCalled()
  })

  it('falls back to the old clipboard when the modern one is absent', async () => {
    // This is the ordinary case on a phone, not an exotic one: the app is served
    // over plain HTTP on a LAN address, which is not a secure context, so
    // `navigator.clipboard` does not exist at all. Only localhost is exempt —
    // which is why the button worked on the dev machine and did nothing on a
    // phone. See CLAUDE.md §8, risk 3.
    vi.stubGlobal('navigator', {})
    const exec = vi.fn(() => true)
    document.execCommand = exec

    expect(await shareVideo({ url: 'u', canShare: true })).toBe('copied')
    expect(exec).toHaveBeenCalledWith('copy')
    expect(document.querySelector('textarea')).toBeNull() // tidied up after
  })

  it('falls through to the old clipboard when the modern one refuses', async () => {
    // Present but rejecting — a permission, or a document that is not focused.
    vi.stubGlobal('navigator', {
      clipboard: {
        writeText: vi.fn(async () => {
          throw new Error('denied')
        }),
      },
    })
    document.execCommand = vi.fn(() => true)

    expect(await shareVideo({ url: 'u', canShare: false })).toBe('copied')
  })

  it('says so when there is no clipboard at all', async () => {
    // A button that does nothing and reports nothing is indistinguishable from
    // a broken one, which is exactly how this was reported.
    vi.stubGlobal('navigator', {})
    document.execCommand = vi.fn(() => false)

    expect(await shareVideo({ url: 'u', canShare: false })).toBe('failed')
  })
})
