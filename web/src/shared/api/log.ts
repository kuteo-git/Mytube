/**
 * Send a line to the server's log.
 *
 * The translation pass lives entirely in this page — it reads the VTT, batches
 * the cues and writes the file back — so when it stops on "Loading subtitles…"
 * there is nothing in any log to read: everything the server records is the
 * result of a decision taken in a browser somewhere else on the LAN. That is
 * what made this fault take three videos to place.
 *
 * The device that hits it most is a phone, which has no console anybody can
 * open. So the lines go to the gateway and come out in logview on :8184
 * (CLAUDE.md §8), beside the six services it already reads.
 *
 * Fire and forget, and never allowed to throw: logging that can break the thing
 * it is watching is worse than no logging. Failures are swallowed on purpose —
 * the log server being unreachable is not something a viewer can act on.
 */
import { apiFetch } from './http'

/** Also written to the browser's console, for whoever does have one open. */
export function log(msg: string, fields: Record<string, unknown> = {}) {
  logAt('info', msg, fields)
}

export function logWarn(msg: string, fields: Record<string, unknown> = {}) {
  logAt('warn', msg, fields)
}

/**
 * Off under the test runner.
 *
 * Not for noise: several suites stub `fetch` and count what the code under test
 * asked for, and a log line is a request. A watcher that changes the count is a
 * watcher that changes the answer.
 */
const silent = import.meta.env?.MODE === 'test'

function logAt(level: string, msg: string, fields: Record<string, unknown>) {
  if (silent) return
  console.log(`[${level}] ${msg}`, fields)
  try {
    void apiFetch('/api/client-log', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ level, msg, fields }),
      // The page may be closing — a line about why is exactly the one worth
      // keeping, and keepalive is what lets it survive the navigation.
      keepalive: true,
    }).catch(() => {})
  } catch {
    /* never let a log line break a player */
  }
}
