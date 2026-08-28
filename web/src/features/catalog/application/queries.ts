import { useProfileScope } from '@/features/identity/application/use-profile-scope'
import { useCallback, useEffect, useRef } from 'react'
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import type { ReactionState } from '../domain/video'
import type { Feed } from '../infrastructure/catalogRepository'
import { hideVideo } from './hidden'
import { videoPollInterval } from './video-poll'
import { httpCatalogRepository as repo } from '../infrastructure/catalogRepository'

/**
 * Application layer — use cases exposed as hooks.
 * These know nothing about HTTP; they only talk to the repository port.
 */

/**
 * The feed is paged all the way down. The gateway hands back an opaque token,
 * so the client never learns whether that is an offset or a cursor.
 */
export function useFeed(topic: string) {
  const me = useProfileScope()
  return useInfiniteQuery({
    queryKey: ['feed', me, topic],
    queryFn: ({ pageParam }) => repo.listFeed(topic, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextPageToken || undefined,
  })
}

/**
 * What is on air, for this member.
 *
 * Refetched on a timer because it is the one list here that goes out of date on
 * its own: the server confirms liveness every ten minutes and stops counting an
 * answer after thirty, so a page left open would otherwise keep a red dot lit
 * over a broadcast that ended an hour ago.
 */
export function useLive() {
  const me = useProfileScope()
  return useQuery({
    queryKey: ['live', me],
    queryFn: () => repo.listLive(),
    // Half the scan interval, so a broadcast starting is noticed within about
    // fifteen minutes end to end and one ending disappears well inside the
    // thirty-minute staleness cut.
    refetchInterval: 5 * 60_000,
    staleTime: 60_000,
  })
}

/**
 * Type-ahead. Debounced by staleTime plus the three-character floor enforced
 * server-side, so typing a sentence costs a handful of queries rather than one
 * per keystroke.
 */
export function useSuggestions(query: string) {
  return useQuery({
    queryKey: ['suggest', query],
    queryFn: () => repo.suggest(query),
    enabled: query.trim().length >= 3,
    staleTime: 60_000,
    placeholderData: (previous) => previous,
  })
}

export function useSearch(query: string) {
  return useInfiniteQuery({
    queryKey: ['search', query],
    queryFn: ({ pageParam }) => repo.search(query, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextPageToken || undefined,
    enabled: query.trim().length > 0,
  })
}

/**
 * Upstream search. Runs on every query, not as a fallback: the library is what
 * the topics chose to bring in, and searching means looking past that.
 */
export function useDiscover(query: string, limit: number) {
  return useQuery({
    queryKey: ['discover', query, limit],
    queryFn: () => repo.discover(query, limit),
    enabled: query.trim().length > 0,
    staleTime: 5 * 60_000,
    // Keep the current results on screen while a larger page is fetched, so
    // asking for more never blanks what is already there.
    placeholderData: (previous) => previous,
  })
}

/** Creates the catalog row for a search result so the watch page can open it. */
export function useOpenExternal() {
  return useMutation({
    mutationFn: (sourceUrl: string) => repo.ensureExternal(sourceUrl),
  })
}

export function useTopics() {
  return useQuery({
    queryKey: ['topics'],
    queryFn: () => repo.listTopics(),
    staleTime: 5 * 60_000,
  })
}

/**
 * Rescans topics.yaml. A pass walks every source, so this is slow by nature;
 * the caller should show it running rather than assume it returns quickly.
 */
export function useRefreshTopics() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => repo.refreshTopics(),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['topics'] })
      void queryClient.invalidateQueries({ queryKey: ['feed'] })
      // The pass that just ran is now the newest row in the history.
      void queryClient.invalidateQueries({ queryKey: ['scans'] })
    },
  })
}

/**
 * The video the watch page is showing. Falls back to writing its metadata when
 * the library has never seen it, so a queue can walk into a channel's back
 * catalogue without every hop having to be ingested up front.
 */
export function useVideo(id: string | undefined) {
  const me = useProfileScope()
  return useQuery({
    queryKey: ['video', me, id],
    queryFn: () => repo.getVideoEnsuring(id!),
    enabled: Boolean(id),
    retry: false,
    // Poll until the media and its subtitles are both in — see video-poll.ts
    // for why the media alone is not enough.
    refetchInterval: (query) =>
      videoPollInterval(query.state.data, query.state.dataUpdateCount),
  })
}

const UP_NEXT_PAGE_SIZE = 20

export function useUpNext(videoId: string | undefined, channelFilter?: string) {
  const me = useProfileScope()
  return useInfiniteQuery({
    queryKey: ['up-next', me, videoId, channelFilter ?? ''],
    queryFn: ({ pageParam }) =>
      repo.listUpNext(videoId!, channelFilter, pageParam, UP_NEXT_PAGE_SIZE),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextPageToken || undefined,
    enabled: Boolean(videoId),
    select: (data) => data.pages.flatMap((p) => p.videos),
  })
}

const COMMENT_PAGE_SIZE = 20

export function useComments(videoId: string | undefined) {
  return useInfiniteQuery({
    queryKey: ['comments', videoId],
    queryFn: ({ pageParam }) => repo.listComments(videoId!, pageParam, COMMENT_PAGE_SIZE),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextPageToken || undefined,
    enabled: Boolean(videoId),
    select: (data) => ({
      comments: data.pages.flatMap((p) => p.comments),
      totalCount: data.pages[0]?.totalCount ?? 0,
    }),
  })
}

/**
 * Where to play a video from. Kept out of the video query because the answer
 * changes on its own — an upstream URL expires, and a background download
 * flips the answer to a local file.
 *
 * The cache window is deliberately far shorter than the upstream URL's own
 * lifetime. Serving a URL that died five minutes ago costs a playback failure;
 * re-resolving costs one cheap request.
 */
export function useStream(videoId: string | undefined) {
  const queryClient = useQueryClient()
  return useQuery({
    queryKey: ['stream', videoId],
    queryFn: async () => {
      const stream = await repo.getStream(videoId!)
      // The gateway found the catalogue claiming a file the disk does not have
      // and put the row right. The copy this page is holding was fetched before
      // that, and nothing else will refresh it: videoPollInterval stops once
      // the media state looks settled, and READY looks settled.
      if (stream.repaired && videoId) {
        void queryClient.invalidateQueries({ queryKey: ['video'] })
      }
      return stream
    },
    enabled: Boolean(videoId),
    staleTime: 5 * 60_000,
    retry: false,
    // Keep asking until the downloaded file exists, then stop.
    //
    // This endpoint is the authority on how a video can be played, so the
    // upgrade from the low-resolution upstream to the local copy should follow
    // from asking it — not, as it did, from spotting the download in a capped
    // list of every job the queue has ever run. A handful of finished jobs
    // pushed the running one off that list and the picture simply stayed at
    // 360p, with no way to tell from the page that anything was wrong.
    // And stop when nothing is coming. With streaming only, `local` never
    // arrives — a three-hour video would ask two thousand times about a file
    // that is never going to exist. The server says so in the same answer
    // rather than the client reading the setting separately: two sources of
    // that truth would disagree eventually, and the failure is either asking
    // for ever or going quiet while a download really is on its way.
    refetchInterval: (query) =>
      query.state.data?.local || query.state.data?.cacheDisabled ? false : 5000,
  })
}

/** How long a pointer must rest on a card before it counts as interest. */
const PREFETCH_HOVER_MS = 250

/**
 * Resolves a video's upstream URL before anyone asks to play it.
 *
 * Resolving costs a yt-dlp process — about 1.4 seconds — and that is the whole
 * of the delay between pressing play and seeing a picture. Paying it while the
 * pointer rests on a card moves the wait somewhere nobody is watching.
 *
 * Hovering is not playing, so this schedules no download; the gateway is told
 * as much. The delay keeps a pointer sweeping across a grid from resolving
 * every card it crosses.
 */
/**
 * Where a pasted channel address leads, if it leads anywhere.
 *
 * Asked for every search, because that is the only way a paste can be noticed
 * without a second control to paste into. It costs nothing for ordinary terms —
 * the gateway recognises an address before it looks anything up — and for a
 * channel the household already follows it costs one local query.
 */
export function useChannelLink(query: string) {
  return useQuery({
    queryKey: ['channel-link', query],
    queryFn: () => repo.resolveChannel(query),
    enabled: query.trim().length > 0,
    staleTime: 10 * 60 * 1000,
  })
}

export function useStreamPrefetch() {
  const queryClient = useQueryClient()
  const timer = useRef<number | undefined>(undefined)

  const cancel = useCallback(() => {
    window.clearTimeout(timer.current)
    timer.current = undefined
  }, [])

  const prefetch = useCallback(
    (videoId: string) => {
      cancel()
      timer.current = window.setTimeout(() => {
        void queryClient.prefetchQuery({
          // A key of its own, and this is the whole of the fix for a video that
          // arrived with no subtitles.
          //
          // It used to be the player's own key, so that pressing play "finds
          // the answer already in cache and never issues a request at all".
          // That is exactly the fault: `?prefetch=1` deliberately does not
          // fetch captions and does not queue a transfer — hovering a card is
          // not choosing a video — and a hover answer sitting under the
          // player's key for five minutes meant pressing play never asked the
          // question that does. Measured on 2JajSt59wqc: every `/stream`
          // request the gateway ever saw for it carried `prefetch=true`, and
          // the video folder was never created at all.
          //
          // Nothing is lost by asking again. What the hover is really warming
          // is ingest's resolve cache, which is on the server and shared by
          // both requests; the second one is answered from it.
          queryKey: ['stream', videoId, 'prefetch'],
          queryFn: () => repo.getStream(videoId, true),
          staleTime: 5 * 60_000,
        })
      }, PREFETCH_HOVER_MS)
    },
    [cancel, queryClient],
  )

  // Cancelling on unmount matters: navigating away from a feed must not leave
  // a timer that resolves a video nobody is looking at any more.
  useEffect(() => cancel, [cancel])

  return { prefetch, cancel }
}

/**
 * Polls while anything is downloading, then stops.
 *
 * `forcePoll` exists because a caller can know a download is coming before the
 * queue does: pressing play schedules the transfer asynchronously, so the first
 * job list is empty and a self-driving interval would shut off before the job
 * ever appears.
 */
export function useIngestJobs(activeOnly = false, forcePoll = false) {
  return useQuery({
    queryKey: ['ingest-jobs', activeOnly],
    queryFn: () => repo.listJobs(activeOnly),
    refetchInterval: (query) => {
      if (forcePoll) return 2000
      const jobs = query.state.data ?? []
      return jobs.some((j) => j.state === 'QUEUED' || j.state === 'RUNNING') ? 2000 : false
    },
  })
}

export function useCancelJob() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (jobId: string) => repo.cancelJob(jobId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['ingest-jobs'] })
    },
  })
}

/**
 * The Activity page's own view of the job list.
 *
 * A separate query key, not a flag on useIngestJobs, because the two views are
 * genuinely different lists: this one hides what has been dismissed and asks
 * for far more rows, while the player's needs every job and fifty of them.
 * Sharing a key would let whichever mounted last overwrite the other's cache —
 * and the losing side would be the player, which uses this list to notice that
 * a download has finished.
 */
export function useActivityJobs(limit = 200) {
  return useQuery({
    queryKey: ['ingest-jobs', 'activity', limit],
    queryFn: () => repo.listJobs(false, { hideDismissed: true, limit }),
    refetchInterval: (query) => {
      const jobs = query.state.data ?? []
      return jobs.some((j) => j.state === 'QUEUED' || j.state === 'RUNNING') ? 2000 : false
    },
  })
}

export function useDismissJob() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (jobId: string) => repo.dismissJob(jobId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['ingest-jobs'] })
    },
  })
}

/** Dismisses all jobs with a given state. Returns the count for toast display. */
export function useDismissJobs() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (state: string) => repo.dismissJobs(state),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['ingest-jobs'] })
    },
  })
}

export function useRetryJob() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (jobId: string) => repo.retryJob(jobId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['ingest-jobs'] })
    },
  })
}

/**
 * What the scanner has been doing, a page at a time.
 *
 * Paged at the server rather than fetched whole: this grows by a row an hour
 * for as long as the service runs, which is the shape of list that has to be
 * paged from the first day rather than the day it becomes a problem.
 */
export function useScans(limit = 10) {
  return useInfiniteQuery({
    queryKey: ['scans', limit],
    initialPageParam: 0,
    queryFn: ({ pageParam }) => repo.listScans(limit, pageParam),
    getNextPageParam: (last, pages) => {
      const loaded = pages.reduce((n, page) => n + page.scans.length, 0)
      return loaded < last.total ? loaded : undefined
    },
  })
}

/**
 * Result of the most recent topic scan. Polls every 2s while a scan is running
 * so the Activity page stays accurate across refreshes and devices.
 */
export function useScanStatus() {
  return useQuery({
    queryKey: ['scan-status'],
    queryFn: () => repo.getScanStatus(),
    refetchInterval: (query) => (query.state.data?.running ? 2000 : false),
    staleTime: 5_000,
  })
}

export function useClearScans() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => repo.clearScans(),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['scans'] })
    },
  })
}

export function useStorage() {
  return useQuery({
    queryKey: ['storage'],
    queryFn: () => repo.getStorage(),
    staleTime: 60_000,
  })
}

export function useHistory() {
  const me = useProfileScope()
  return useInfiniteQuery({
    queryKey: ['history', me],
    queryFn: ({ pageParam }) => repo.listHistory(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextPageToken || undefined,
  })
}

export function useSaved() {
  const me = useProfileScope()
  return useInfiniteQuery({
    queryKey: ['pinned', me],
    queryFn: ({ pageParam }) => repo.listPinned(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextPageToken || undefined,
  })
}

// Playlists are a small list read whole, so a plain query rather than an
// infinite one. The videos inside a playlist page, and that query is separate.
export function usePlaylists() {
  const me = useProfileScope()
  return useQuery({
    queryKey: ['playlists', me],
    queryFn: () => repo.listPlaylists(),
  })
}

export function usePlaylist(id: string) {
  const me = useProfileScope()
  return useInfiniteQuery({
    queryKey: ['playlist', id, me],
    queryFn: ({ pageParam }) => repo.getPlaylist(id, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextPageToken || undefined,
    enabled: Boolean(id),
  })
}

// No create, rename, delete, or add/remove hooks. Playlists and Watch later are
// a read-only mirror of the member's YouTube account, refreshed on every account
// scan — a write here would be reverted by the next pass.

// `enabled` exists for the playback queue, which calls this unconditionally —
// hooks must be — but only wants the request when Watch later is the list being
// played through.
export function useWatchLater(enabled = true) {
  const me = useProfileScope()
  return useInfiniteQuery({
    queryKey: ['watch-later', me],
    queryFn: ({ pageParam }) => repo.listWatchLater(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextPageToken || undefined,
    enabled,
  })
}

export function useSetPinned() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ videoId, pinned }: { videoId: string; pinned: boolean }) =>
      repo.setPinned(videoId, pinned),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['pinned'] })
      void queryClient.invalidateQueries({ queryKey: ['video'] })
    },
  })
}

/**
 * The shape react-query keeps for a paged feed. Named here because the cache is
 * edited directly below, and an edit against an untyped cache is an edit that
 * cannot be checked.
 */
interface InfiniteFeed {
  pages: Feed[]
  pageParams: unknown[]
}

/**
 * "I have already seen this."
 *
 * Hidden locally so it goes at once, and recorded as fully watched so the
 * server agrees: the ranker already drops anything past 85% from the home page,
 * so telling it the truth is what makes this outlive the browser it was pressed
 * in. The local list is what makes it instant, and what covers the seconds
 * before the feed is next fetched.
 */
export function useMarkWatched() {
  return useMutation({
    mutationFn: ({ videoId, durationSeconds }: { videoId: string; durationSeconds: number }) =>
      repo.recordProgress(videoId, Math.max(0, Math.floor(durationSeconds)), 1),
    onMutate: ({ videoId }) => {
      hideVideo(videoId, 'watched')
    },
  })
}

export function useNotInterested() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (videoId: string) => repo.recordNotInterested(videoId),
    onMutate: (videoId) => {
      // Persist across refreshes — the server signal is fire-and-forget and
      // may not have committed before the next page load.
      hideVideo(videoId, 'not-interested')

      // Remove instantly from every cached feed page.
      queryClient.setQueriesData<InfiniteFeed>(
        { queryKey: ['feed'] },
        (old) => {
          if (!old) return old
          return {
            ...old,
            pages: old.pages.map((page) => ({
              ...page,
              videos: page.videos.filter((v) => v.id !== videoId),
            })),
          }
        },
      )
    },
  })
}

/**
 * Reacting changes both the like count and, through the recorded signal, the
 * next feed — so the feed cache is invalidated alongside the video itself.
 */
export function useSetReaction(videoId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (reaction: ReactionState) => repo.setReaction(videoId, reaction),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['video'] })
      void queryClient.invalidateQueries({ queryKey: ['feed'] })
    },
  })
}

export function useChannel(channelId: string | undefined) {
  return useQuery({
    queryKey: ['channel', channelId],
    queryFn: () => repo.getChannel(channelId!),
    enabled: Boolean(channelId),
  })
}

/**
 * A channel's uploads, live from YouTube. Not served from the catalog: a scan
 * only ever brings in the newest few dozen videos, and capping the channel page
 * at that would look like the channel itself had no more.
 *
 * `sortToken` selects an ordering. It is passed as the first page token because
 * that is how YouTube models it — an ordering is just another continuation —
 * and it is part of the query key so switching order refetches from the top
 * rather than appending a differently-sorted page to the current one.
 */
export function useChannelVideos(channelId: string | undefined, sortToken?: string) {
  return useInfiniteQuery({
    queryKey: ['channel-videos', channelId, sortToken ?? ''],
    queryFn: ({ pageParam }) => repo.listChannelVideos(channelId!, pageParam),
    initialPageParam: sortToken ?? '',
    getNextPageParam: (lastPage) => lastPage.nextPageToken || undefined,
    enabled: Boolean(channelId),
  })
}

/**
 * The videos this viewer has spent the most time on. Doubles as a playlist:
 * the order is the play order.
 */
export function useTopPlayed(limit?: number, enabled = true) {
  return useQuery({
    queryKey: ['top-played', limit ?? 0],
    queryFn: () => repo.listTopPlayed(limit),
    enabled,
    staleTime: 60_000,
  })
}

/**
 * Widely-watched videos, filtered to this viewer's taste and rotated on a fixed
 * clock server-side — so a reload does not reshuffle it, but returning later
 * shows something else.
 */
export function usePopular(limit?: number) {
  return useQuery({
    queryKey: ['popular', limit ?? 0],
    queryFn: () => repo.listPopular(limit),
    staleTime: 5 * 60_000,
  })
}

export function useSubscriptions() {
  const me = useProfileScope()
  return useQuery({
    queryKey: ['subscriptions', me],
    queryFn: () => repo.listSubscriptions(),
    staleTime: 60_000,
  })
}

/**
 * Subscribing is not only a ranking signal here: it registers the channel as a
 * content source, so the scanner starts bringing its uploads in. The feed cache
 * is invalidated for the first effect; the second lands on the next scan.
 */
export function useSetSubscription(channelId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (subscribed: boolean) => repo.setSubscription(channelId, subscribed),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['channel', channelId] })
      void queryClient.invalidateQueries({ queryKey: ['subscriptions'] })
      void queryClient.invalidateQueries({ queryKey: ['feed'] })
      // The watch page's button reads the channel off the video, not off the
      // channel query — so without this it kept saying Subscribe until the
      // page was reloaded, while the server had already recorded the change.
      void queryClient.invalidateQueries({ queryKey: ['video'] })
      void queryClient.invalidateQueries({ queryKey: ['up-next'] })
    },
  })
}

export function useAddComment(videoId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (text: string) => repo.addComment(videoId, text),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['comments', videoId] })
    },
  })
}

export function useFetchComments(videoId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => repo.fetchComments(videoId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['comments', videoId] })
    },
  })
}
