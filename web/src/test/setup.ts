import '@testing-library/jest-dom/vitest'
import { afterEach } from 'vitest'
import { cleanup } from '@testing-library/react'

afterEach(cleanup)

// jsdom has no layout engine and ships neither observer. A no-op is the
// difference between "the test failed" and "the test could not run at all".
class NoopObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
  takeRecords() {
    return []
  }
}

globalThis.ResizeObserver ??= NoopObserver as unknown as typeof ResizeObserver

/**
 * A drivable IntersectionObserver.
 *
 * jsdom cannot decide whether anything intersects, so the choice is between a
 * no-op — which leaves the entire scroll-to-miniplayer path untestable, and that
 * is precisely the path that broke — and letting tests say when the crossing
 * happens. This does the latter: it records live observers and their targets,
 * and `fireIntersection` delivers an entry to whoever is watching that element.
 *
 * It reports only `isIntersecting`, because that is the only field the code
 * under test reads. Faking ratios and rectangles would be inventing numbers no
 * layout produced, and asserting on those would be testing this file.
 */
type Watcher = { callback: IntersectionObserverCallback; targets: Set<Element> }
const watchers = new Set<Watcher>()

class DrivableIntersectionObserver {
  private watcher: Watcher

  constructor(callback: IntersectionObserverCallback) {
    this.watcher = { callback, targets: new Set() }
    watchers.add(this.watcher)
  }

  observe(target: Element) {
    this.watcher.targets.add(target)
  }

  unobserve(target: Element) {
    this.watcher.targets.delete(target)
  }

  disconnect() {
    watchers.delete(this.watcher)
  }

  takeRecords() {
    return []
  }
}

globalThis.IntersectionObserver =
  DrivableIntersectionObserver as unknown as typeof IntersectionObserver

/** Tells every observer watching `target` that it did or did not intersect. */
export function fireIntersection(target: Element, isIntersecting: boolean) {
  for (const watcher of watchers) {
    if (!watcher.targets.has(target)) continue
    watcher.callback(
      [{ target, isIntersecting } as unknown as IntersectionObserverEntry],
      null as unknown as IntersectionObserver,
    )
  }
}

afterEach(() => watchers.clear())

// jsdom does not implement media playback and throws on these, which is fatal
// as soon as a test renders the player at all.
HTMLMediaElement.prototype.play = function play() {
  return Promise.resolve()
}
HTMLMediaElement.prototype.pause = function pause() {}

// jsdom in this configuration exposes a `localStorage` that has no methods on
// it, and the app reads preferences during render — so components throw before
// they can be asserted on. An in-memory Storage is closer to a browser's than
// the stub is, and it resets between files because the environment does.
if (typeof window.localStorage?.getItem !== 'function') {
  const store = new Map<string, string>()
  const memoryStorage: Storage = {
    get length() {
      return store.size
    },
    key: (i) => [...store.keys()][i] ?? null,
    getItem: (k) => store.get(k) ?? null,
    setItem: (k, v) => void store.set(k, String(v)),
    removeItem: (k) => void store.delete(k),
    clear: () => store.clear(),
  }
  Object.defineProperty(window, 'localStorage', {
    value: memoryStorage,
    configurable: true,
  })
  Object.defineProperty(window, 'sessionStorage', {
    value: memoryStorage,
    configurable: true,
  })
}

globalThis.matchMedia ??= ((query: string) => ({
  matches: false,
  media: query,
  onchange: null,
  addEventListener() {},
  removeEventListener() {},
  addListener() {},
  removeListener() {},
  dispatchEvent: () => false,
})) as unknown as typeof matchMedia
