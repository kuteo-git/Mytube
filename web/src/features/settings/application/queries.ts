import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { FeedMix } from '@/features/settings/domain/feed-mix'
import {
  settingsRepository,
  type StoredFeedMix,
  type TranslateConfig,
  type TranslateTestResult,
} from '@/features/settings/infrastructure/settingsRepository'

export function useVoices() {
  return useQuery({
    queryKey: ['tts-voices'],
    queryFn: () => settingsRepository.listVoices(),
    // The synthesiser's voice list does not change while the app is open.
    staleTime: Infinity,
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

export function useTranslateModels() {
  return useMutation({
    mutationFn: ({ baseUrl, apiKey }: { baseUrl: string; apiKey: string }) =>
      settingsRepository.listModels(baseUrl, apiKey),
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
