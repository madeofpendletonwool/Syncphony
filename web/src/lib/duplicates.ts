import type { QueueItem } from './playback'

type Song = Pick<QueueItem['track'], 'provider' | 'trackId' | 'title' | 'artists'>

/** A title or artist, loosely: case, accents, "(Remastered 2011)" and punctuation aside. */
function loose(s: string) {
  return s
    .normalize('NFKD')
    .replace(/\p{M}/gu, '')
    .toLowerCase()
    .replace(/\s*[([].*?[)\]]/g, '')
    .replace(/\s+-\s+.*$/, '')
    .replace(/[^\p{L}\p{N}]+/gu, ' ')
    .trim()
}

/** The same song: the same track, or the same title by the same artist on another service. */
export function sameSong(a: Song, b: Song) {
  if (a.provider === b.provider && a.trackId === b.trackId) return true
  const artist = (s: Song) => loose(s.artists[0] ?? '')
  return loose(a.title) !== '' && loose(a.title) === loose(b.title) && artist(a) === artist(b)
}

/** The copy of a song already waiting or playing in the room, if there is one. */
export function queuedCopy(items: readonly QueueItem[], song: Song) {
  return items.find((i) => (i.state === 'queued' || i.state === 'playing') && sameSong(i.track, song))
}
