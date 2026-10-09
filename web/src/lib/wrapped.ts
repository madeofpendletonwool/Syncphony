import type { Recap } from './playlists'

// Syncphony Wrapped (MAD-722): a night's recap as a story, a page at a
// time, like the year-end ones, plus an image card to share.

export type WrappedPage = 'intro' | 'crown' | 'people' | 'artists' | 'mix' | 'overlap' | 'skipped' | 'outro'

/** The pages a night's story has: only the ones it has something to say on. */
export function wrappedPages(r: Recap): WrappedPage[] {
  const pages: WrappedPage[] = ['intro']
  if (r.night?.songOfTheNight || r.mostHearted) pages.push('crown')
  if (r.topAdder || r.streak) pages.push('people')
  if (r.stats.topArtists.length > 0 || r.stats.topTracks.length > 0) pages.push('artists')
  if (r.genres.length > 0 || r.decades.length > 0) pages.push('mix')
  if (r.overlaps.length > 0) pages.push('overlap')
  if (r.mostSkipped) pages.push('skipped')
  pages.push('outro')
  return pages
}

/** How long each page stays up before the story moves on. */
export const PAGE_MS = 7000

/** Each mix's shares as fractions of the biggest, for bars. */
export function shareBars(shares: { name: string; plays: number }[]) {
  const top = Math.max(1, ...shares.map((s) => s.plays))
  return shares.map((s) => ({ ...s, fraction: s.plays / top }))
}

/** "Ann and Bo matched on Radiohead, Björk and 2 more." */
export function overlapLine(names: [string, string], artists: string[]) {
  const shown = artists.slice(0, 2)
  const more = artists.length - shown.length
  const list = more > 0 ? `${shown.join(', ')} and ${more} more` : shown.length === 2 ? `${shown[0]} and ${shown[1]}` : shown[0]
  return `${names[0]} and ${names[1]} matched on ${list}`
}

/** The range a night's recap covers, for the recap query. `to` reaches just past its end. */
export function nightRange(n: { startedAt: string; endedAt: string }) {
  return { from: n.startedAt, to: new Date(Date.parse(n.endedAt) + 1).toISOString() }
}
