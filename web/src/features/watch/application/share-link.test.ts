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

    expect(await shareVideo({ url: 'u', canShare: true })).toBe('failed')
    expect(writeText).not.toHaveBeenCalled()
  })

  it('says so when the clipboard is refused', async () => {
    // Plain HTTP on a LAN address is not a secure context, and the clipboard is
    // one of the APIs that withholds itself there — see CLAUDE.md §2. Reporting
    // it beats a button that silently does nothing.
    vi.stubGlobal('navigator', {
      clipboard: {
        writeText: vi.fn(async () => {
          throw new Error('denied')
        }),
      },
    })

    expect(await shareVideo({ url: 'u', canShare: false })).toBe('failed')
  })
})
