import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import type { ReactionState } from '../domain/video'
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
  return useInfiniteQuery({
    queryKey: ['feed', topic],
    queryFn: ({ pageParam }) => repo.listFeed(topic, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextPageToken || undefined,
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
    },
  })
}

export function useVideo(id: string | undefined) {
  return useQuery({
    queryKey: ['video', id],
    queryFn: () => repo.getVideo(id!),
    enabled: Boolean(id),
  })
}

export function useUpNext(videoId: string | undefined, channelFilter?: string) {
  return useQuery({
    queryKey: ['up-next', videoId, channelFilter ?? ''],
    queryFn: () => repo.listUpNext(videoId!, channelFilter),
    enabled: Boolean(videoId),
  })
}

export function useComments(videoId: string | undefined) {
  return useQuery({
    queryKey: ['comments', videoId],
    queryFn: () => repo.listComments(videoId!),
    enabled: Boolean(videoId),
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
  return useQuery({
    queryKey: ['stream', videoId],
    queryFn: () => repo.getStream(videoId!),
    enabled: Boolean(videoId),
    staleTime: 5 * 60_000,
    retry: false,
  })
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

/**
 * Result of the most recent topic scan. Scans are manual or twelve-hourly, so
 * this does not poll; the Activity page refetches it when the user asks.
 */
export function useScanStatus() {
  return useQuery({
    queryKey: ['scan-status'],
    queryFn: () => repo.getScanStatus(),
    staleTime: 30_000,
  })
}

export function useStorage() {
  return useQuery({
    queryKey: ['storage'],
    queryFn: () => repo.getStorage(),
    staleTime: 60_000,
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
      void queryClient.invalidateQueries({ queryKey: ['video', videoId] })
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

export function useChannelVideos(channelId: string | undefined) {
  return useInfiniteQuery({
    queryKey: ['channel-videos', channelId],
    queryFn: ({ pageParam }) => repo.listChannelVideos(channelId!, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextPageToken || undefined,
    enabled: Boolean(channelId),
  })
}

export function useSubscriptions() {
  return useQuery({
    queryKey: ['subscriptions'],
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
