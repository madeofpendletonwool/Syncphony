import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'

export type Suggestion = components['schemas']['Suggestion']
export type SuggestionSeed = components['schemas']['SuggestionSeed']
export type VibeScope = components['schemas']['Suggestions']['scope']

/** How many songs a vibe list shows. */
export const VIBE_LIMIT = 12

/**
 * Songs to keep the room's vibe going. `song` is the song playing (or
 * nothing), so the list follows the room from song to song but stays put
 * while you add from it. `shuffle` > 0 asks for a new list.
 */
export const suggestionsQuery = (roomId: string, scope: VibeScope, song: string | undefined, shuffle = 0) =>
  queryOptions({
    queryKey: ['suggestions', roomId, scope, song ?? '', shuffle],
    queryFn: ({ signal }) =>
      unwrap(
        api.GET('/rooms/{roomId}/suggestions', {
          params: { path: { roomId }, query: { scope, limit: VIBE_LIMIT, refresh: shuffle > 0 } },
          signal,
        }),
      ),
    staleTime: 5 * 60_000,
  })

/**
 * Why a song is suggested, for its byline: "Like Heroes", and in the group's
 * vibe, whose song that was.
 */
export function becauseLabel(seed: SuggestionSeed, scope: VibeScope, meId: string, nameOf: (userId: string) => string | undefined) {
  const like = `Like ${seed.title}`
  if (scope === 'mine') return like
  if (seed.userId === meId) return `${like}, your pick`
  const name = nameOf(seed.userId)
  return name ? `${like}, ${name}'s pick` : like
}
