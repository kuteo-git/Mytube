import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  settingsRepository,
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
