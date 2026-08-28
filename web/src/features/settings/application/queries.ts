import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { FeedMix } from '@/features/settings/domain/feed-mix'
import type { RankingSettings } from '@/features/settings/domain/ranking'
import {
  settingsRepository,
  type StoredFeedMix,
  type TranscriptConfig,
  type TranscriptTestResult,
  type TranslateConfig,
  type TranslateTestResult,
} from '@/features/settings/infrastructure/settingsRepository'

/**
 * Where speech is synthesised, and what to ask it for.
 *
 * This replaces a `useVoices` that fetched the synthesiser's own list. OpenAI
 * publishes no endpoint that lists voices, so the app cannot ask a question
 * that only one provider can answer — the voice is typed instead, and this
 * carries the endpoint that will be given it.
 */
export function useTTSConfig() {
  return useQuery({
    queryKey: ['tts-config'],
    queryFn: () => settingsRepository.getTTSConfig(),
  })
}

/**
 * Try the settings before committing them.
 *
 * Takes the form's current values rather than what is stored: testing after
 * saving is testing the thing you have already accepted, which is the wrong way
 * round when the point is to find out whether to accept it.
 */
export function useTestTTS() {
  return useMutation({
    mutationFn: (input: { baseUrl: string; model: string; apiKey: string; voice: string }) =>
      settingsRepository.testTTS(input),
  })
}

export function useSaveTTSConfig() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: { baseUrl: string; model: string; apiKey: string; voice: string }) =>
      settingsRepository.saveTTSConfig(input),
    onSuccess: (saved) => client.setQueryData(['tts-config'], saved),
  })
}

export function useTranslateConfig() {
  return useQuery({
    queryKey: ['translate-config'],
    queryFn: () => settingsRepository.getTranslateConfig(),
  })
}

export function useSaveTranslateConfig() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: { baseUrl: string; model: string; apiKey: string }) =>
      settingsRepository.saveTranslateConfig(input),
    onSuccess: (saved: TranslateConfig) => {
      client.setQueryData(['translate-config'], saved)
      // The translation cache is partitioned by model, so the player has to be
      // told the model changed or it would go on reading the old partition.
      void client.invalidateQueries({ queryKey: ['video'] })
    },
  })
}

export function useFeedMix() {
  return useQuery({
    queryKey: ['feed-mix'],
    queryFn: () => settingsRepository.getFeedMix(),
    // One file on the gateway; it changes only when somebody presses Save here.
    staleTime: Infinity,
  })
}

export function useSaveFeedMix() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (mix: FeedMix) => settingsRepository.saveFeedMix(mix),
    onSuccess: (saved) => {
      client.setQueryData(['feed-mix'], (old: StoredFeedMix | undefined) =>
        old ? { ...old, ...saved } : old,
      )
      // The feed is frozen into a snapshot for half an hour so that paging
      // stays stable, which means a saved change would otherwise be invisible
      // until it expired — indistinguishable from the setting not working.
      // Dropping the cached feed sends the next request without a page token,
      // and the ranker starts a new snapshot under the new mix.
      void client.invalidateQueries({ queryKey: ['feed'] })
    },
  })
}

/**
 * How many videos each share has to choose from.
 *
 * Its own query, loaded after the sliders rather than with them. It costs a full
 * ranking pass, and the sliders are useful without it — so a slow or failed
 * count should cost the reader a line of context, not the setting.
 */
export function useBucketSizes() {
  return useQuery({
    queryKey: ['feed-mix-buckets'],
    queryFn: () => settingsRepository.getBucketSizes(),
    // Changes as the library grows, but nothing on this page changes it.
    staleTime: 5 * 60 * 1000,
    retry: false,
  })
}

export function useRanking() {
  return useQuery({
    queryKey: ['ranking'],
    queryFn: () => settingsRepository.getRanking(),
    staleTime: Infinity,
  })
}

export function useSaveRanking() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (settings: RankingSettings) => settingsRepository.saveRanking(settings),
    onSuccess: (saved) => {
      client.setQueryData(['ranking'], saved)
      // Same reason the mix does it: the feed is frozen into a snapshot, so a
      // change would otherwise stay invisible until it expired and look exactly
      // like a setting that does nothing.
      void client.invalidateQueries({ queryKey: ['feed'] })
      // The fresh-subscribed share is one of these settings, and it decides how
      // much the three mix sliders divide — so their readouts are now stale.
      void client.invalidateQueries({ queryKey: ['feed-mix'] })
      void client.invalidateQueries({ queryKey: ['feed-mix-buckets'] })
    },
  })
}

export function useTranslateModels() {
  return useMutation({
    mutationFn: ({ baseUrl, apiKey }: { baseUrl: string; apiKey: string }) =>
      settingsRepository.listModels(baseUrl, apiKey),
  })
}

export function useTranscriptConfig() {
  return useQuery({
    queryKey: ['transcript-config'],
    queryFn: () => settingsRepository.getTranscriptConfig(),
  })
}

export function useSaveTranscriptConfig() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: { baseUrl: string; apiKey: string; clearBaseUrl?: boolean }) =>
      settingsRepository.saveTranscriptConfig(input),
    onSuccess: (saved: TranscriptConfig) => {
      client.setQueryData(['transcript-config'], saved)
    },
  })
}

export function useTestTranscript() {
  return useMutation<
    TranscriptTestResult,
    Error,
    { baseUrl: string; apiKey: string; videoId: string }
  >({
    mutationFn: (input) => settingsRepository.testTranscript(input),
  })
}

export function useTestTranslate() {
  return useMutation<
    TranslateTestResult,
    Error,
    { baseUrl: string; model: string; apiKey: string }
  >({
    mutationFn: (input) => settingsRepository.testTranslate(input),
  })
}
