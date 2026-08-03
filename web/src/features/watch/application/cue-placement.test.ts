import { describe, expect, it } from 'vitest'
import { centreCue, centreCues } from './cue-placement'

describe('centreCue', () => {
  it('undoes the placement YouTube auto-captions carry', () => {
    // Every cue of a measured file arrived as align:start position:0%, which
    // pins it bottom-left while a cue with no settings centres.
    const cue = { align: 'start', position: 0, line: 90, size: 50 }
    centreCue(cue)
    expect(cue).toEqual({
      align: 'center',
      position: 'auto',
      line: 'auto',
      size: 100,
    })
  })

  it('leaves an already-centred cue as it is', () => {
    const cue = { align: 'center', position: 'auto' as const, line: 'auto' as const, size: 100 }
    centreCue(cue)
    expect(cue.align).toBe('center')
  })

  it('applies what it can when a property refuses to be set', () => {
    // Browsers disagree about which of these are writable. One throwing must
    // not stop the others — align is the one that matters most.
    const cue = {
      align: 'start',
      get position(): number | 'auto' {
        return 0
      },
      set position(_v: number | 'auto') {
        throw new Error('read-only in this engine')
      },
    }
    expect(() => centreCue(cue)).not.toThrow()
    expect(cue.align).toBe('center')
  })
})

describe('centreCues', () => {
  it('walks a cue list', () => {
    const cues = [
      { align: 'start', position: 0 },
      { align: 'start', position: 0 },
    ]
    expect(centreCues(cues)).toBe(2)
    expect(cues.every((c) => c.align === 'center')).toBe(true)
  })

  it('copes with a track that has not loaded its cues', () => {
    expect(centreCues(null)).toBe(0)
  })
})
