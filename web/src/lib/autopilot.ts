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

/** Why autopilot chose a song, for a byline: "Like Heroes". */
export function autopilotReason(pick: AutopilotPick) {
  if (!pick.seedTitle) return 'To keep the music going'
  return `Like ${pick.seedTitle}`
}
