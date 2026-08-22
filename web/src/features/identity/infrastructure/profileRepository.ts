import { apiJSON } from '@/shared/api/http'
import type { Profile } from '../domain/profile'

/** What deleting a profile would take with it. */
export interface ProfileUsage {
  subscriptions: number
  watched: number
  reactions: number
  saved: number
  watchLater: number
  playlists: number
  comments: number
  signals: number
}

export interface ProfileRepository {
  list(): Promise<Profile[]>
  create(name: string): Promise<Profile>
  /**
   * Counts what would go, without removing any of it.
   *
   * Answered by the same query that then does the deleting, asked with
   * `dry_run` — so the numbers in the confirmation are the numbers that go,
   * rather than a second definition of "what belongs to this profile" drifting
   * quietly out of step with the first.
   */
  usage(id: string): Promise<ProfileUsage>
  remove(id: string): Promise<void>
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

  async usage(id) {
    return apiJSON<ProfileUsage>(`/api/profiles/${encodeURIComponent(id)}/usage`)
  },

  async remove(id) {
    await apiJSON<{ deleted: string }>(`/api/profiles/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    })
  },
}
