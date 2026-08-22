/**
 * The source language.
 *
 * Every value here is the string as it was written in the component it came
 * from — this file is an extraction, not a rewrite. `vi.ts` mirrors its shape
 * exactly, and `dictionaries.test.ts` fails the build if it stops doing so.
 *
 * Nested by feature, matching the slices, so a key's home is obvious from where
 * it is used. `common` is for words that genuinely appear in several unrelated
 * places; anything used twice inside one feature stays in that feature.
 */
export const en = {
  common: {
    cancel: 'Cancel',
    save: 'Save',
    saved: 'Saved',
    retry: 'Retry',
    delete: 'Delete',
    loading: 'Loading…',
    loadMore: 'Load more',
    close: 'Close',
    back: 'Back',
    moreOptions: 'More options',
    moreActions: 'More actions',
  },

  nav: {
    home: 'Home',
    saved: 'Saved',
    history: 'History',
    storage: 'Storage',
    settings: 'Settings',
    activity: 'Activity',
    subscriptions: 'Subscriptions',
    youtubeAccount: 'YouTube account',
    watchLater: 'Watch later',
    playlists: 'Playlists',
    account: 'Account',
    toggleSidebar: 'Toggle sidebar',
    searchPlaceholder: 'Search the library',
    clearSearch: 'Clear search',
    homeFeed: 'Home feed',
    topics: 'Topics',
    profile: 'Profile',
  },

  chips: {
    all: 'All',
    live: 'Live',
    scrollLeft: 'Scroll categories left',
    scrollRight: 'Scroll categories right',
  },

  card: {
    live: 'LIVE',
    suggested: 'Suggested',
    markWatched: 'Mark as watched',
    markedWatched: 'Marked as watched',
    notInterested: 'Not interested',
    removed: 'Removed — press to fetch again',
    couldNotOpen: 'Could not open that video.',
  },

  language: {
    label: 'Language',
  },
} as const

/**
 * The same keys, any strings.
 *
 * `as const` above is what gives i18next its typed keys — `t('nope')` does not
 * compile — but it also makes every *value* a literal type, so a dictionary
 * typed as `typeof en` would demand the English words back and reject every
 * translation. Recursive, so nesting a section deeper later does not silently
 * stop being checked.
 */
type Translated<T> = { [K in keyof T]: T[K] extends string ? string : Translated<T[K]> }

export type Dictionary = Translated<typeof en>
