/**
 * Who is watching.
 *
 * Not an account and not a login. §2 of the charter is two to five people in one
 * household with no public sign-up, and §3 leaves media URLs unprotected because
 * the LAN is trusted — a password here would be a second, stricter trust model
 * guarding a library whose video files anyone on the network can already fetch
 * by URL.
 *
 * What it is for is separation, not protection: whose subscriptions these are,
 * whose likes, whose watch history, whose feed. Every one of those tables is
 * already keyed by a user id; until now nothing ever sent one, so the whole
 * household shared a single profile without anybody choosing to.
 */
export interface Profile {
  id: string
  name: string
}

/**
 * The id a browser reports when nobody has chosen.
 *
 * Empty rather than a guess. The gateway already falls back to `DEV_USER_ID`
 * when the header is absent, and that fallback is what keeps every existing
 * install working the moment this ships — inventing an id here would quietly
 * strand the history that install already has.
 */
export const NO_PROFILE = ''

/** Whether a name can be used. Trimmed, non-empty, and short enough to render. */
export function validProfileName(name: string): boolean {
  const trimmed = name.trim()
  return trimmed.length > 0 && trimmed.length <= 40
}
