import { useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useIngestJobs } from './queries'
import type { IngestJob } from '../infrastructure/catalogRepository'

/**
 * Tracks the background copy of one video.
 *
 * The gateway answers "how do I play this" with either the local file or an
 * upstream stream, and that answer changes the moment a download finishes. The
 * client cannot know when that happens by watching the video element, so it
 * watches the job instead and re-asks — which is what makes playback move from
 * upstream to disk without the viewer doing anything.
 */
export function useDownloadProgress(videoId: string | undefined): IngestJob | undefined {
  const { data: jobs } = useIngestJobs(false)
  const queryClient = useQueryClient()

  const job = jobs?.find((j) => j.videoId === videoId)
  const finished = job?.state === 'SUCCEEDED'

  useEffect(() => {
    if (!videoId || !finished) return
    // The copy landed: the stream answer and the media state are both stale.
    void queryClient.invalidateQueries({ queryKey: ['stream', videoId] })
    void queryClient.invalidateQueries({ queryKey: ['video', videoId] })
  }, [videoId, finished, queryClient])

  return job
}
