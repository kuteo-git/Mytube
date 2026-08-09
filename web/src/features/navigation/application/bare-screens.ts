/**
 * Screens that carry their own chrome on a phone.
 *
 * A phone's bottom bar holds the places you move *between* while browsing —
 * Home, Subscriptions, History, Settings. Everything else is somewhere you
 * arrive on purpose, and arriving somewhere on purpose is a screen of its own:
 * no search bar, no tab bar, a back arrow and the name of what you are looking
 * at. That is what a phone does everywhere, and it is the difference between a
 * page you are inside and a page you are passing through.
 *
 * One list, because three things read it and they must not disagree: the shell
 * decides which bars to draw, the player decides where its own bar rests, and
 * the back bar decides what to call the screen. The last time two of those
 * worked it out separately, the miniplayer floated a tab bar's height above the
 * bottom of a channel page.
 */

/**
 * What the back bar should call each screen, or null where the screen names
 * itself.
 *
 * A channel is the null: `ChannelHeader` already gives the name in large type,
 * and the bar fades its own copy in only once that has scrolled away — which is
 * behaviour the page owns rather than something a table can say.
 */
const TITLES: Array<[test: (path: string) => boolean, title: string | null]> = [
  [(p) => p.startsWith('/channel/'), null],
  [(p) => p === '/saved', 'Saved'],
  [(p) => p === '/storage', 'Storage'],
  [(p) => p === '/activity', 'Activity'],
  [(p) => p === '/settings/feed', 'Home feed'],
  [(p) => p === '/settings/advanced', 'Advanced'],
  [(p) => p === '/settings/narration', 'Narration'],
  [(p) => p === '/settings/translation', 'Translation'],
]

/** Whether this path is one of those screens. */
export function isBareScreen(pathname: string): boolean {
  return TITLES.some(([test]) => test(pathname))
}

/**
 * The title for the shell's back bar, or null when the screen draws its own.
 *
 * Null for two different reasons that must not be confused: a path with no
 * entry here is not a bare screen at all, while a channel is one that names
 * itself. Callers ask `isBareScreen` first.
 */
export function bareTitle(pathname: string): string | null {
  return TITLES.find(([test]) => test(pathname))?.[1] ?? null
}
