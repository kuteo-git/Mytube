import type { ParseKeys } from 'i18next'

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

  search: {
    placeholder: 'Search the library',
    clear: 'Clear search',
    toggleSidebar: 'Toggle sidebar',
  },

  profiles: {
    whoIsThis:
      'Who this browser is. Subscriptions, history and recommendations are kept per person; the video library is shared.',
    enterName: 'Enter a name',
    couldNotAdd: 'Could not add that name',
    newProfileName: 'New profile name',
    deleteTitle: 'Delete {{name}}?',
    counting: 'Counting what this profile holds…',
    couldNotRead: 'Could not read what this profile holds.',
    couldNotDelete: 'Could not delete that profile.',
    removesFor: 'This removes, for {{name}} only:',
    nothingYet: 'This profile has not watched or saved anything yet.',
    librarySurvives:
      'The videos and channels themselves stay — they belong to the whole household. This cannot be undone.',
    deleting: 'Deleting…',
    counts: {
      subscriptions: 'subscriptions',
      watched: 'videos watched',
      playlists: 'playlists',
      reactions: 'likes and dislikes',
      saved: 'saved videos',
      watchLater: 'in Watch later',
      comments: 'comments',
    },
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

  pages: {
    home: {
      continueWatching: 'Continue watching',
    },
    history: { title: 'Watch history' },
    saved: { title: 'Saved videos' },
    watch: { notFound: 'Video not found.' },
    channel: {
      notFound: 'Channel not found.',
      sort: 'Sort videos',
      unreachable: 'Could not reach YouTube to list this channel.',
      noUploads: 'This channel has no uploads.',
    },
    search: {
      fromLink: 'Video from a link',
      inLibrary: 'In your library',
      noMatches: 'Nothing here matches.',
      youtubeUnreachable: 'Could not reach YouTube.',
      noMore: 'No further results.',
    },
    activity: {
      historyCleared: 'Scan history cleared',
      inProgress: 'In progress',
      scanNow: 'Scan now',
      neverScanned: 'No scan has run yet.',
      retryDownload: 'Retry download',
      cancelDownload: 'Cancel download',
      queued: 'Waiting its turn',
    },
    storage: {
      softCeiling: 'Soft ceiling for autoremoval',
      freeOnDisk: 'Free on disk',
      videosOnDisk: 'Videos on disk',
      nextRemoved: 'Next to be removed',
    },
    watchLater: {
      title: 'Watch later',
      signedOut:
        'YouTube signed you out, so this list is not being brought across. Paste your cookies again in Settings.',
      empty:
        'Nothing here yet. This list is a copy of your YouTube Watch later, brought across on each account scan.',
    },
    playlists: {
      wontOpen: 'YouTube will not open this one',
      waitingSession: 'Waiting for a YouTube session',
      notReadYet: 'Not read yet',
      wontOpenLong:
        'YouTube lists this playlist but will not open it — it answers "the playlist does not exist". Nothing here can fix that; it is asked once and then left alone.',
      notReadYetLong:
        'This playlist has not been read from YouTube yet. It fills in on one of the next account scans.',
      emptyUpstream: 'This playlist is empty on YouTube.',
    },
  },

  settings: {
    feedMix: {
      title: 'Home feed',
      couldNotRead:
        'Could not read the current mix. The gateway may be running an older build that does not have this setting yet.',
      intro:
        'Where the new videos on your home page come from. The three add up to one page, so raising one lowers the others.',
      subscribed: 'Channels you follow',
      affinity: 'More of what you watch',
      affinityHint: 'Channels you have not subscribed to, on subjects you keep coming back to.',
      discovery: 'Something new',
      discoveryHint: 'Outside your usual subjects. Set this to zero and none will appear.',
      savedRebuilt: 'Saved — your feed has been rebuilt.',
      couldNotSave: 'Could not save. Is the gateway running?',
      emptyBucket: 'Nothing in your library fits this right now, so its places go to the other two.',
      thin: 'Only {{count}} videos fit this, so a share this large repeats them or reaches well down the list.',
    },    ranking: {
      sessionBlend: {
        label: 'Follow what you are watching now',
        hint: 'How much the last few videos of this sitting outweigh your whole watch history. At zero the page ignores today entirely; at the top it is almost the Next rail.',
      },
      freshSubscribed: {
        label: 'Room kept for new uploads',
        hint: 'A share of every page reserved for videos your channels published recently, so a new upload never has to win a place on score alone.',
      },
      freshnessWindow: {
        label: 'How long a video counts as new',
        hint: 'Both the reserved share above and the boost that surfaces breaking news use this.',
      },
      maxAge: {
        label: 'Oldest video the home page will show',
        hint: 'Older videos stay reachable through search and on their channel; they just do not fill the grid.',
      },
      recencyHalfLife: {
        label: 'How fast newly added videos fade',
        hint: 'A video the library has just fetched leads the grid, then settles. This is how long it takes to lose half of that lift.',
      },
      temperature: {
        label: 'How closely the order follows the score',
        hint: 'Lower keeps the best videos at the top every time; higher lets close scores trade places between visits. Near zero the page looks identical on every refresh.',
      },
      poolSize: {
        label: 'How many videos enter the draw',
        hint: 'Only this many of each share are shuffled; the rest stay in score order. Raising it far is what let videos scoring below zero onto the first page.',
      },
      unit: {
        thisSitting: '{{value}}% this sitting',
        ofPage: '{{value}}% of the page',
        hours: '{{value}} hours',
        days: '{{value}} days',
        months: '{{value}} months',
        years: '{{value}} years',
        videos: '{{value}} videos',
        plain: '{{value}}',
      },
    },

    advanced: {
      couldNotRead:
        'Could not read the ranking settings. The gateway may be running an older build that does not have them yet.',
      intro:
        'How the home feed ranks, rather than what it is made of. Anything you do not set here follows the built-in value, so a setting left alone keeps up if the ranker changes.',
      breaks: 'Moving this far can visibly break the ordering.',
      builtIn: 'Built in: {{value}}',
    },
    storage: {
      title: 'Library folder',
      description: 'Where downloaded videos are kept, and whether to keep them at all.',
      couldNotRead: 'Could not read the storage settings.',
      couldNotReach: 'Could not reach the server to check that folder.',
      savedRestart:
        'Saved. Restart the app for it to take effect — three services read this when they start.',
      couldNotSaveFolder: 'Could not save that folder.',
      couldNotChange: 'Could not change that setting.',
      savedHere: 'Saved here.',
    },
    model: {
      name: 'model name',
      chooseFromList: 'Choose from the list',
      noMatch: 'no match',
      reload: 'Reload the model list',
      type: 'Type a model name',
    },
    couldNotSave: 'Could not save',
  },

  account: {
    openMenu: 'Account',
    watching: 'Watching',
    manageProfiles: 'Manage profiles',
    youtubeAccount: 'YouTube account',
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

/**
 * Every key that exists, as a dotted path.
 *
 * For the places that carry a key rather than a word — a domain file naming
 * what a slider is called, a constant array of navigation labels. Typed as
 * `string` those would accept a misspelling and render the key on screen, in
 * both languages, reported by nothing; that is exactly what this caught the
 * first time it compiled.
 */
// i18next's own key type rather than one derived here. Two derivations of the
// same set agree until they do not, and the first time they disagreed the
// error was four lines of unions with no way to see which key was missing.
export type TranslationKey = ParseKeys<'translation'>
