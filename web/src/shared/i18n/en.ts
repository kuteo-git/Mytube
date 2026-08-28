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
    mediaRootUnavailable: 'The media drive is not available. Check that it is connected.',
    deleteSelf: 'Switch to another profile before deleting this one.',
    deleteLast: 'This is the only profile. Add another before deleting it.',
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
      'Who this browser is. Subscriptions, history and recommendations are kept per person; the video library is shared.',    keepsSeparate: 'Keeps subscriptions, history and recommendations separate. The library itself is shared.',

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
    watched: 'Watched',
    unsave: 'Unsave',
    onYouTube: 'On YouTube',
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
      explainer:
        'These three divide {{adjustable}}% of the page. The rest is kept for videos you are part way through ({{continue}}%), ones you have finished and might want again ({{rewatch}}%), and new uploads from channels you follow ({{fresh}}%). None of those three is a taste — the first two are your watch history, and the last is how you find out a channel posted — so they are not divided here.',
      resetToDefault: 'Reset to default',
      defaults: 'Defaults',
      ofWindow: '{{count}} of {{window}}',
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
      useBuiltIn: 'Use built-in values',
      careful: 'careful',
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

  player: {
    findingStream: 'Finding a stream…',    streamFailedDownloading:
      'Live streaming failed. Downloading instead — {{percent}}%, it will start by itself.',

    streamFailedQueued:
      'Live streaming failed. The download is queued behind another video, and this will start by itself once it finishes.',
    streamFailed: 'The stream could not be loaded.',
    notStartedYet: 'This broadcast has not started yet. It will begin playing on its own.',
    evicted:
      'The media file was removed to reclaim disk space, and upstream has nothing directly playable. Re-download it to watch again.',
    nothingPlayable: 'Nothing playable is available yet. The download has to finish first.',
    noFile: 'No media file available yet.',
    streaming: 'Streaming from upstream while the copy is fetched',
    copyQueued: 'Copy queued',
    downloadProgress: 'Download progress',
    nextVideo: 'Next video',
    goToLive: 'Go to live',
    live: 'LIVE',
    pictureInPicture: 'Picture in picture',
    fullScreen: 'Full screen',
    expand: 'Expand player',
    closePlayer: 'Close player',
    readAloud: 'Read aloud',
    vietnameseNarration: 'Vietnamese narration',
    unavailable: {
      membersOnly:
        'This video is members-only on YouTube. Join the channel there to watch it — it cannot be fetched into the library.',
      private: 'This video is private on YouTube, so it cannot be fetched.',
      removed: 'This video has been removed from YouTube, so it cannot be fetched.',
      generic: 'YouTube will not hand this video over, so it cannot be fetched.',
    },
    speech: {
      notStarted: 'Speech not started',
      preparing: 'Preparing speech…',
      waitingTranslation: 'Waiting for translation…',
      unavailable: 'Speech service unavailable — retrying',
      ready: 'Speech ready',
    },
    translation: {
      notStarted: 'Not started',
      waitingSettings: 'Waiting for translator settings…',
      noModel: 'No translation model configured — set one in Settings',
      readingSaved: 'Reading saved translations…',
      loadingSubtitles: 'Loading subtitles…',
      preparingCues: 'Preparing cues…',
      noSubtitles: 'No subtitles available',
      alreadyVietnamese: 'Already Vietnamese — nothing to translate',      failedWith: 'Translation failed: {{error}}',

      failed: 'Translation failed — nothing came back',
    },
  },

  comments: {
    count: '{{count}} Comments',
    placeholder: 'Add a comment...',
    label: 'Add a comment',
    noneReturned: 'YouTube did not return comments for this video.',
    couldNotLoad: 'Could not load YouTube comments.',
    like: 'Like comment',
    dislike: 'Dislike comment',
  },

  description: {
    showMore: '…more',
    state: {
      ABSENT: 'Not downloaded',
      DOWNLOADING: 'Downloading',
      READY: 'On disk',
      EVICTED: 'Removed to free space',
      FAILED: 'Download failed',
      UNAVAILABLE: 'YouTube will not hand it over',
    },
    onDisk: 'On disk',
    addedToLibrary: 'Added to library',
    mediaState: 'Media state',
    showLess: 'Show less',
  },

  equalizer: {
    offInFullscreen: 'EQ off in fullscreen',
    offWhileDownloading: 'EQ off while the video is still downloading',
    alsoOffInFullscreen: 'Also off in fullscreen',
    alsoOffWhileDownloading: 'Also off while downloading',
    preset: 'Equalizer preset',
    dryWet: 'Dry wet mix',
  },

  queue: {
    machineVietnamese: 'Tiếng Việt (dịch máy) — translated as you watch',
    watchLater: 'Watch later',
    topPlayed: 'Top played',
  },

  youtubeAccount: {
    installStep: 'Install <0>Get cookies.txt LOCALLY</0> for Chrome — the one yt-dlp own FAQ recommends.',
    warning: 'Do not install "Get cookies.txt" without LOCALLY — that one was removed from the store as malware.',
    disconnect: 'Disconnect',
    reconnect: 'Reconnect',
    summary: '{{subscriptions}} subscriptions, {{playlists}} playlists, {{videos}} videos.',
    title: 'YouTube account',
    description:
      'Brings your own subscriptions, playlists and liked videos into the library. Your account, on this machine only.',
    signedOut: 'Signed out — paste your cookies again',
    notConnected: 'Not connected',
    howTo: 'Open youtube.com signed in, click the extension, choose Netscape format.',
    pasteBelow: 'Paste the whole file below.',
    cookiesFile: 'Cookies file',
    scanNow: 'Scan now',
  },

  narration: {
    notConfigured: 'No speech service set — add one in Settings › Narration.',
    baseURL: 'Where speech is synthesised',
    openaiFormat:
      "Anything that speaks OpenAI's audio API: its own /v1, or any service that copies it. The /v1 is optional here; both forms work.",
    modelHint: 'Sent as-is. Services that have no models ignore it.',
    voiceHint: 'Typed, because providers do not agree on a list. Per device.',
    tryIt:
      'Open a video and come back here to hear these against it — the player keeps going in the corner.',
    voiceVolume: 'Voice volume',
    voiceVolumeHint: 'Goes past 100% because synthesised speech is quieter than film audio.',
    videoVolumeWhileSpeaking: 'Video volume while speaking',
  },

  proxySettings: {
    title: 'Proxy',
    description:
      'YouTube blocks by address, not by request — so when it refuses this house, the only thing that helps is asking from somewhere else. Measured here: subtitles were refused every time directly and answered every time through a proxy.',
    url: 'Proxy',
    urlHint:
      'The whole address in one line, as your provider gives it: scheme://user:password@host:port. http, https, socks5 and socks5h all work. A rotating residential proxy is the kind that helps; datacentre ranges are blocked as a block.',
    enabled: 'Use the proxy',
    enabledHint: 'Off sends everything from this house as usual. Your address is kept.',
    forCaptions: 'Subtitles',
    forCaptionsHint: 'Tens of kilobytes a video. This is what the proxy is for.',
    forListings: 'Metadata and search',
    forListingsHint: 'Channel scans, search, playlists. Small, but a great many of them.',
    forComments: 'Comments',
    forCommentsHint: 'One video at a time, only when you open them.',
    forMedia: 'Video downloads and playback',
    forMediaHint: 'Hundreds of megabytes a video. Leave off unless you know why you need it.',
    mediaWarning:
      'Video is thousands of times larger than everything else here. A single film can spend more of your proxy allowance than a month of subtitles. Turn this on only for videos that will not download any other way.',
    mediaConfirm: 'I understand, turn it on',
    directAddress: 'Without proxy',
    proxyAddress: 'Through proxy',
    gotCues: 'Subtitles came back: {{language}}, {{count}} lines',
    testFailed: 'The test could not be run.',
    saved: 'Saved.',
  },
  proxyError: {
    proxy_url_missing: 'Type a proxy address first.',
    proxy_url_unparseable: 'That is not an address this can read.',
    proxy_url_scheme: 'Start with http://, https://, socks5:// or socks5h://.',
    proxy_url_no_host: 'The address has no host in it.',
    proxy_unreachable:
      'The proxy did not carry the request. Check the username, password and port.',
    proxy_not_changing_address:
      'The proxy works but leaves your address unchanged, so YouTube sees the same house. Ask your provider for a rotating residential endpoint.',
    captions_refused: 'The proxy works, and YouTube still refused this address.',
    captions_empty: 'Answered, but with no subtitles in it.',
  },
  translationSettings: {
    openaiFormat:
      "Anything that speaks OpenAI's chat completions API: its own /v1, or any service that copies it — OmniRouter, a local runner. The /v1 is optional here; both forms work.",
    description:
      'Where subtitles are translated. Changing the model translates fresh — earlier translations are kept, so switching back costs nothing.',
    baseURL: 'Base URL',
    apiKey: 'API key',
    noKeyStored: 'No key stored yet.',
    hideKey: 'Hide the API key',
    showKey: 'Show the API key',
    couldNotLoadModels: 'Could not load the model list.',
    testFailed: 'The test call did not get through.',
    savedNextBatch: 'Saved. The next batch uses it.',
  },

  upNext: {
    all: 'All',
    fromChannel: 'From {{name}}',
    nextIn: 'Next: {{title}}',
    title: 'Up next',
    nothingQueued: 'Nothing queued',
    expand: 'Expand up next',
    collapse: 'Collapse up next',
    filter: 'Filter suggestions',
    playingFromQueue: 'Playing from queue',
  },

  actions: {
    linkCopied: 'YouTube link copied',
    couldNotCopy: "Couldn't copy the link",
  },

  storageMode: {
    fromEnvironment: 'From the environment — nothing saved yet.',
    streamOnly: 'Stream only, keep nothing',
  },

  phoneSettings: {
    library: 'Library',
    account: 'Account',
    preferences: 'Preferences',
    settings: 'Settings',
    narration: 'Narration',
    translation: 'Translation',
    advanced: 'Advanced',
  },

  ui: {
    jobsCleared_failed: '{{count}} failed jobs cleared',
    jobsCleared_completed: '{{count}} completed jobs cleared',
    percentOfBudget: '{{percent}}% of budget',
    writable: 'Writable. {{free}} free, {{count}} videos already there.',
    fits: '{{count}} videos fit this.',
    viewMore: 'View more ({{count}})',
    playingFrom: 'Playing from {{name}}',
    keyStored: 'A key ending {{hint}} is stored. Leave blank to keep it.',
    videoCount: '{{count}} videos',
    add: 'Add',
    adding: 'Adding…',
    advanced: 'Advanced',
    audio: 'Audio',
    auto: 'Auto',
    autoAtHeight: 'Auto ({{height}})',
    autoplay: 'Autoplay',
    budget: 'Budget',
    categories: 'Categories',
    check: 'Check',
    checking: 'Checking…',
    comments: 'Comments',
    completed: 'Completed',
    connect: 'Connect',
    connected: 'Connected',
    dislike: 'Dislike',
    dismiss: 'Dismiss',
    downloads: 'Downloads',
    dryWet: 'Dry/Wet',
    environment: 'Environment',
    evicted: 'Evicted',
    failed: 'Failed',
    kept: 'Kept',
    like: 'Like',
    main: 'Main',
    model: 'Model',
    mute: 'Mute',
    name: 'Name',
    narration: 'Narration',
    off: 'Off',
    pause: 'Pause',
    play: 'Play',
    playlist: 'Playlist',
    preamp: 'Preamp',
    profile: 'Profile',
    queue: 'Queue',
    refreshing: 'Refreshing',
    resolution: 'Resolution',
    savedFull: 'Saved.',
    saving: 'Saving…',
    scanning: 'Scanning…',
    scans: 'Scans',
    search: 'Search',
    seek: 'Seek',
    seeking: 'Seeking…',
    settings: 'Settings',
    share: 'Share',
    shared: 'Shared',
    status: 'Status:',
    subscribe: 'Subscribe',
    subscribed: 'Subscribed',
    subtitles: 'Subtitles',
    test: 'Test',
    testing: 'Testing…',
    translated: 'Translated',
    translating: 'Translating…',
    translation: 'Translation',
    unmute: 'Unmute',
    unsaved: 'Unsaved',
    used: 'Used',
    verified: 'Verified',
    voice: 'Voice',
    volume: 'Volume',
    whosWatching: "Who's watching?",
  },

  empty: {
    what_playlists: 'playlists',
    what_history: 'history',
    what_savedVideos: 'saved videos',
    what_watchLater: 'Watch later',
    what_storageUsage: 'storage usage',
    history: 'No watch history yet. Videos will appear here after you watch them.',
    saved: 'No saved videos yet. Keep a video from its menu or the Storage page.',
    subscriptions: 'No subscriptions yet. Open a channel and press Subscribe to follow it.',
    downloads: 'Nothing has been downloaded yet. Pressing play on a video schedules a copy.',
    noEvictable:
      'No videos are currently eligible for automatic removal. Every downloaded video is either pinned or recently watched.',
    couldNotLoad: 'Could not load {{what}}. Is the gateway running?',
    couldNotReachLibrary: 'Could not reach the library service. Is the gateway running?',
  },

  storagePage: {
    fillsPast:
      'When storage fills past {{budget}}, the least recently watched unpinned videos are removed from disk. Their metadata and history are kept.',
    bannerFull:
      'Storage is {{percent}}% full ({{used}} of {{budget}}). The least recently watched videos will be removed from disk automatically; their metadata and history are kept.',
    manage: 'Manage storage',
    addSomeone: 'Add someone',
  },

  more: {
    linesProgress: '{{done}}/{{total}} lines',
    etaLeft: '{{eta}} left',
    tooFastLines: '{{count}} lines too long to speak in time',
    milliseconds: '{{ms}} ms',
    loadingMore: 'Loading more',
    loadMore: 'Load more',
    countInLibrary: '{{count}} in your library',
    mixOf: 'Mix — the {{count}} you play most',
    videoSuggestionCount: '{{count}} videos',
    useBuiltInShort: 'use built-in',
    pinnedByName: 'Pinned by {{name}}',
    upNextInSeconds: 'Up next in {{seconds}}',
    scanLine: '{{sources}} sources · {{seen}} videos seen · {{added}} added',
    linesLeft: '{{done}}/{{total}} lines',
    secondsLeft: '{{seconds}} left',
    lineTooLong: 'line {{index}} too long to speak in time',
    newBadgeShort: 'New',
    savedByAnyone: 'saved by someone, never removed',
    lastScanResult: 'Last scan: {{result}}',
    inYourLibrary: 'in your library',
    mix: 'Mix',
    mixOfWhat: 'Mix — the {{count}} you play most',
    folder: 'Folder',
    videosAreAt: '{{total}} videos are at {{path}}. Changing the folder does not move them — they would have to be downloaded again.',
    keepThemFirst: 'To keep them, move them yourself first, then change this.',
    changeAnyway: 'Change anyway',
    streamOnlyHint: 'Videos play from YouTube and are not downloaded. Subtitles still arrive, files already here still play, and Retry still works.',
    lastScan: 'Last scan:',
    signedOutBanner: 'YouTube signed you out — subscriptions are no longer updating.',
    lowerPreamp: 'Lower the preamp if boosted bands distort.',
    upNextIn: 'Up next in',
    pinnedBy: 'Pinned by',
    reply: 'Reply',
    tryAgain: 'Try again',
    clearAll: 'Clear all',
    scanSummary: '{{sources}} sources · {{seen}} videos seen · {{added}} added',
    everythingShown: 'That is everything your Home feed is set to show. <0>Adjust the mix</0> to widen it.',
    nothingYetHome: 'Nothing here yet. Topics are scanned every 12 hours; use Refresh to scan now. <0>Check your Home feed mix</0> — a share set to 0% shows nothing at all.',
    playlistUnopenable: 'That playlist could not be opened. It may have been deleted, or it belongs to another account.',
    noPlaylistsYet: 'No playlists yet. Connect your YouTube account in Settings and they arrive on the next scan.',
    linkNotVideo: 'That link does not lead to a video or a channel. Playlist links cannot be opened here yet.',
    resultsFor: 'Results for',
    onYouTube: 'On YouTube',
    modelsAvailable: '{{count}} models available.',
    videosCount: '{{count}} videos',
    newBadge: 'New',
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
