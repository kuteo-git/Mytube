import { apiJSON } from '@/shared/api/http'
import type { Profile } from '../domain/profile'

export interface ProfileRepository {
  list(): Promise<Profile[]>
  create(name: string): Promise<Profile>
}

export const httpProfileRepository: ProfileRepository = {
  async list() {
    const { profiles } = await apiJSON<{ profiles: Profile[] }>('/api/profiles')
    return profiles ?? []
  },

  async create(name: string) {
    const { profile } = await apiJSON<{ profile: Profile }>('/api/profiles', {
      method: 'POST',
      body: JSON.stringify({ name }),
    })
    return profile
  },
}
