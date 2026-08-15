import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { BANDS } from './eq-presets'

/**
 * A stand-in for Web Audio, recording what was built and what it was connected
 * to. Only the parts `audio-graph.ts` touches, and one behaviour copied
 * faithfully on purpose: `createMediaElementSource` throws the second time it is
 * handed the same element, which is what the module's once-per-element rule
 * exists to avoid.
 */
class FakeParam {
  value = 0
  targets: number[] = []
  setTargetAtTime(value: number) {
    this.value = value
    this.targets.push(value)
  }
  setValueAtTime(value: number) {
    this.value = value
  }
}

class FakeNode {
  outputs: FakeNode[] = []
  connect(node: FakeNode) {
    this.outputs.push(node)
    return node
  }
  disconnect() {
    this.outputs = []
  }
}

class FakeGain extends FakeNode {
  gain = new FakeParam()
}

class FakeFilter extends FakeNode {
  type = ''
  frequency = new FakeParam()
  Q = new FakeParam()
  gain = new FakeParam()
}

class FakeContext {
  static sources = new Set<unknown>()
  static gains: FakeGain[] = []
  static filters: FakeFilter[] = []

  state: AudioContextState = 'suspended'
  currentTime = 0
  destination = new FakeNode()
  resume = vi.fn(async () => {
    this.state = 'running'
  })
  addEventListener = vi.fn()

  createGain() {
    const g = new FakeGain()
    FakeContext.gains.push(g)
    return g
  }
  createBiquadFilter() {
    const f = new FakeFilter()
    FakeContext.filters.push(f)
    return f
  }
  createMediaElementSource(el: unknown) {
    if (FakeContext.sources.has(el)) {
      throw new Error('already connected')
    }
    FakeContext.sources.add(el)
    return new FakeNode()
  }
}

async function loadModule() {
  vi.resetModules()
  FakeContext.sources = new Set()
  FakeContext.gains = []
  FakeContext.filters = []
  vi.stubGlobal('AudioContext', FakeContext)
  return import('./audio-graph')
}

function media(): HTMLMediaElement {
  return document.createElement('video')
}

beforeEach(() => {
  vi.unstubAllGlobals()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('the graph', () => {
  it('builds one filter per band, in the order the bands are declared', async () => {
    const { getAudioContext } = await loadModule()
    getAudioContext()
    expect(FakeContext.filters).toHaveLength(BANDS.length)
    expect(FakeContext.filters.map((f) => f.type)).toEqual(BANDS.map((b) => b.kind))
    expect(FakeContext.filters.map((f) => f.frequency.value)).toEqual(
      BANDS.map((b) => b.frequency),
    )
  })

  it('starts transparent, so switching the equaliser on cannot be what breaks it', async () => {
    const { getAudioContext } = await loadModule()
    getAudioContext()
    expect(FakeContext.filters.every((f) => f.gain.value === 0)).toBe(true)
  })

  it('keeps one context across calls', async () => {
    const { getAudioContext } = await loadModule()
    expect(getAudioContext()).toBe(getAudioContext())
  })
})

describe('attachElement', () => {
  it('routes an element exactly once, however often it is asked', async () => {
    // The invariant the whole design rests on. `createMediaElementSource` throws
    // on a second call for one element and cannot be undone, and this is called
    // from an effect in a component that re-renders freely — so a guard that
    // failed here would take the player's sound out and there would be no way
    // back short of a reload.
    const { attachElement } = await loadModule()
    const el = media()
    expect(attachElement(el)).toBe(true)
    expect(attachElement(el)).toBe(true)
    expect(attachElement(el)).toBe(true)
    expect(FakeContext.sources.size).toBe(1)
  })

  it('routes both layers separately', async () => {
    const { attachElement, isAttached } = await loadModule()
    const a = media()
    const b = media()
    attachElement(a)
    attachElement(b)
    expect(FakeContext.sources.size).toBe(2)
    expect(isAttached(a)).toBe(true)
    expect(isAttached(b)).toBe(true)
  })

  it('starts an element silent, so a hidden layer is never heard on the way in', async () => {
    const { attachElement } = await loadModule()
    attachElement(media())
    const elementGain = FakeContext.gains[FakeContext.gains.length - 1]
    expect(elementGain.gain.value).toBe(0)
  })

  it('reports nothing attached when there is no Web Audio at all', async () => {
    // The player reads this to decide whether the volume slider drives a gain
    // node or the element's own volume. Getting it wrong on a television with no
    // Web Audio means a volume control that does nothing.
    vi.resetModules()
    vi.stubGlobal('AudioContext', undefined)
    vi.stubGlobal('webkitAudioContext', undefined)
    const { attachElement, isAttached, getAudioContext } = await import('./audio-graph')
    const el = media()
    expect(attachElement(el)).toBe(false)
    expect(isAttached(el)).toBe(false)
    expect(getAudioContext()).toBeNull()
  })

  it('says no to nothing', async () => {
    const { attachElement, isAttached } = await loadModule()
    expect(attachElement(null)).toBe(false)
    expect(isAttached(undefined)).toBe(false)
  })
})

describe('setElementGain', () => {
  it('gives each layer its own level', async () => {
    const { attachElement, setElementGain } = await loadModule()
    const front = media()
    const hidden = media()
    attachElement(front)
    const frontGain = FakeContext.gains[FakeContext.gains.length - 1]
    attachElement(hidden)
    const hiddenGain = FakeContext.gains[FakeContext.gains.length - 1]

    setElementGain(front, 0.8)
    setElementGain(hidden, 0)

    expect(frontGain.gain.value).toBeCloseTo(0.8)
    expect(hiddenGain.gain.value).toBe(0)
  })

  it('refuses a level below silence', async () => {
    const { attachElement, setElementGain } = await loadModule()
    const el = media()
    attachElement(el)
    const gain = FakeContext.gains[FakeContext.gains.length - 1]
    setElementGain(el, -2)
    expect(gain.gain.value).toBe(0)
  })

  it('ignores an element that never made it into the graph', async () => {
    const { setElementGain, getAudioContext } = await loadModule()
    getAudioContext()
    expect(() => setElementGain(media(), 0.5)).not.toThrow()
  })
})

describe('applyEq', () => {
  it('writes each band to its own filter', async () => {
    const { getAudioContext, applyEq } = await loadModule()
    getAudioContext()
    const gains = BANDS.map((_, i) => i - 4)
    applyEq({ enabled: true, gains, preamp: -6, preset: 'custom' })
    expect(FakeContext.filters.map((f) => f.gain.value)).toEqual(gains)
  })

  it('flattens every band when switched off rather than rerouting anything', async () => {
    // "Off" and "on, flat" are deliberately the same signal path — see
    // buildGraph. If this ever started disconnecting nodes, the two states could
    // fail differently and the working one would hide the broken one.
    const { getAudioContext, applyEq } = await loadModule()
    getAudioContext()
    applyEq({ enabled: true, gains: BANDS.map(() => 8), preamp: -8, preset: 'custom' })
    applyEq({ enabled: false, gains: BANDS.map(() => 8), preamp: -8, preset: 'custom' })
    expect(FakeContext.filters.every((f) => f.gain.value === 0)).toBe(true)
  })

  it('turns the preamp into a linear multiplier', async () => {
    const { getAudioContext, applyEq } = await loadModule()
    getAudioContext()
    const preamp = FakeContext.gains[FakeContext.gains.length - 1]
    applyEq({ enabled: true, gains: BANDS.map(() => 0), preamp: -6, preset: 'custom' })
    expect(preamp.gain.value).toBeCloseTo(0.501, 3)
  })

  it('does nothing at all before a graph exists', async () => {
    const { applyEq } = await loadModule()
    expect(() =>
      applyEq({ enabled: true, gains: BANDS.map(() => 3), preamp: 0, preset: 'custom' }),
    ).not.toThrow()
    expect(FakeContext.filters).toHaveLength(0)
  })
})

describe('resumeAudio', () => {
  it('asks a suspended context to start', async () => {
    const { getAudioContext, resumeAudio } = await loadModule()
    const ctx = getAudioContext() as unknown as FakeContext
    resumeAudio()
    expect(ctx.resume).toHaveBeenCalled()
  })

  it('does not pester one that is already running', async () => {
    const { getAudioContext, resumeAudio } = await loadModule()
    const ctx = getAudioContext() as unknown as FakeContext
    ctx.state = 'running'
    resumeAudio()
    expect(ctx.resume).not.toHaveBeenCalled()
  })

  it('is safe before anything built a context', async () => {
    const { resumeAudio } = await loadModule()
    expect(() => resumeAudio()).not.toThrow()
  })
})
