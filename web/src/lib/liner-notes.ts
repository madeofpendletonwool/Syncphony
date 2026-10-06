import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { ApiError, unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'

// Liner notes (MAD-717): release, credits and facts from MusicBrainz, and
// the artist's bio from Wikipedia. The server caches them; the first ask
// for a song can take a few seconds.

export type LinerNotes = components['schemas']['LinerNotes']
export type LinerNotesFact = components['schemas']['LinerNotesFact']

/** A queued song's liner notes, or null when MusicBrainz doesn't know it. */
export const linerNotesQuery = (roomId: string, itemId: string) =>
  queryOptions({
    queryKey: ['liner-notes', roomId, itemId],
    queryFn: async () => {
      try {
        return await unwrap(
          api.GET('/rooms/{roomId}/queue/{itemId}/liner-notes', { params: { path: { roomId, itemId } } }),
        )
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return null
        throw e
      }
    },
    staleTime: Infinity,
    retry: (n, e) => !(e instanceof ApiError && e.status < 500) && n < 2,
  })

/** "Album · 1991 · DGC": the release line. */
export function releaseLine(n: LinerNotes) {
  const parts: string[] = []
  if (n.release) parts.push(n.release.title)
  const year = n.year ?? yearOf(n.release?.date)
  if (year) parts.push(String(year))
  if (n.release?.labels.length) parts.push(n.release.labels.slice(0, 2).join(', '))
  return parts.join(' · ')
}

function yearOf(date: string | undefined) {
  const y = date ? Number(date.slice(0, 4)) : NaN
  return Number.isFinite(y) && y > 0 ? y : undefined
}

/**
 * Cards for the big screen to rotate through between lyrics: the facts,
 * the release, the main credits, and the artist's bio, in that order.
 */
export type LinerCard = { kind: string; title: string; text: string }

export function linerCards(n: LinerNotes): LinerCard[] {
  const cards: LinerCard[] = n.facts.map((f) => ({ kind: f.kind, title: factTitle(f.kind), text: f.text }))
  const release = releaseLine(n)
  if (release) cards.push({ kind: 'release', title: n.release?.type ?? 'Released', text: release })
  for (const c of n.credits.slice(0, 3)) cards.push({ kind: 'credit', title: c.role, text: c.names.join(', ') })
  if (n.artist?.bio) cards.push({ kind: 'bio', title: `About ${n.artist.name}`, text: firstSentences(n.artist.bio, 220) })
  return cards
}

function factTitle(kind: LinerNotesFact['kind']) {
  switch (kind) {
    case 'cover':
      return 'Cover version'
    case 'live':
      return 'Live'
    case 'samples':
      return 'Samples'
    case 'sampled_by':
      return 'Sampled'
    case 'origin':
      return 'Origin story'
    case 'first_released':
      return 'First released'
  }
}

/** As many whole sentences as fit in max characters (at least one). */
export function firstSentences(text: string, max: number) {
  const sentences = text.match(/[^.!?]+[.!?]+(\s|$)/g) ?? [text]
  let out = ''
  for (const s of sentences) {
    if (out && out.length + s.length > max) break
    out += s
  }
  return out.trim()
}
