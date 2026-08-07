/**
 * Deterministic hue from an id, with a random per-session offset so the colour
 * of every card shifts together across page loads — stable within a session,
 * different on the next refresh.
 *
 * Thumbnails and channel avatars are placeholders until the ingest worker
 * stores real image files. Deriving the colour from the id keeps every card
 * visually stable across reloads without inventing a field in the API — colour
 * is a presentation concern, so it is computed here rather than served.
 */
let sessionOffset: number | undefined

export function hueFromId(id: string): number {
  if (sessionOffset === undefined) sessionOffset = Math.random() * 360

  // FNV-1a. A weaker hash makes sequential ids ("v1", "v2", …) collapse onto
  // neighbouring hues, which turned the whole grid a single shade of green.
  let hash = 0x811c9dc5
  for (let i = 0; i < id.length; i++) {
    hash ^= id.charCodeAt(i)
    hash = Math.imul(hash, 0x01000193) >>> 0
  }
  // Spread across the wheel rather than taking the low bits, which cluster.
  return (Math.floor((hash / 0xffffffff) * 360) + sessionOffset) % 360
}
