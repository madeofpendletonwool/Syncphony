import type { components } from '@/api/schema.gen'

export type AutopilotPick = components['schemas']['AutopilotPick']

type Item = { addedBy: string; autopilot?: AutopilotPick }

/**
 * Whether userId queued item. Autopilot's songs are nobody's, even though
 * addedBy names whose taste seeded them.
 */
export function isMine(item: Item, userId: string) {
  return item.addedBy === userId && !item.autopilot
}

/**
 * Why autopilot chose a song, for a byline: the DJ's reason ("Because
 * Sam's Radiohead → Portishead (similar 0.82)"), else "Like Heroes".
 */
export function autopilotReason(pick: AutopilotPick) {
  if (pick.reason) return pick.reason
  if (!pick.seedTitle) return 'To keep the music going'
  return `Like ${pick.seedTitle}`
}

/** Where the DJ's reason came from, for a credit: "via Last.fm, Deezer". */
export function autopilotSource(pick: AutopilotPick) {
  return pick.reason && pick.source ? `via ${pick.source}` : undefined
}
