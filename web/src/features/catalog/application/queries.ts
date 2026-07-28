import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ReactionState } from '../domain/video'
import { httpCatalogRepository as repo } from '../infrastructure/catalogRepository'

/**
 * Application layer — use cases exposed as hooks.
 * These know nothing about HTTP; they only talk to the repository port.
 */

export function useFeed(topic: string) {
  return useQuery({
    queryKey: ['feed', topic],
    queryFn: () => repo.listFeed(topic),
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
 */
export function useStream(videoId: string | undefined) {
  return useQuery({
    queryKey: ['stream', videoId],
    queryFn: () => repo.getStream(videoId!),
    enabled: Boolean(videoId),
    staleTime: 30 * 60_000,
    retry: false,
  })
}

/** Polls while anything is downloading, then stops. */
export function useIngestJobs(activeOnly = false) {
  return useQuery({
    queryKey: ['ingest-jobs', activeOnly],
    queryFn: () => repo.listJobs(activeOnly),
    refetchInterval: (query) => {
      const jobs = query.state.data ?? []
      return jobs.some((j) => j.state === 'QUEUED' || j.state === 'RUNNING') ? 2000 : false
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

export function useAddComment(videoId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (text: string) => repo.addComment(videoId, text),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['comments', videoId] })
    },
  })
}
