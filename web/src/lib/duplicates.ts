import type { components } from '@/api/schema.gen'
import { isMine } from './autopilot'
import { relativeTime } from './time'

export type QueueDuplicate = components['schemas']['QueueDuplicate']

/** An add the server held back: these songs are already in the room, or played recently. */
export class DuplicatesFound extends Error {
  readonly duplicates: QueueDuplicate[]

  constructor(duplicates: QueueDuplicate[]) {
    super('duplicate')
    this.name = 'DuplicatesFound'
    this.duplicates = duplicates
  }
}

/** Where the room's copy is: "playing now", "already in Sam’s lane", "played 20 minutes ago". */
export function whereIs(d: QueueDuplicate, meId: string, nameOf: (userId: string) => string | undefined, now = Date.now()) {
  const it = d.item
  if (d.playedAt) return `played ${relativeTime(d.playedAt, now)}`
  if (it.state === 'playing') return 'playing now'
  if (it.autopilot) return 'already up next'
  if (isMine(it, meId)) return 'already in your lane'
  return `already in ${nameOf(it.addedBy) ?? 'someone'}’s lane`
}

/** The warning to show before adding anyway. */
export function duplicateMessage(dups: readonly QueueDuplicate[], meId: string, nameOf: (userId: string) => string | undefined, now = Date.now()) {
  if (dups.length === 1) return `“${dups[0].title}” ${isOrWas(whereIs(dups[0], meId, nameOf, now))}`
  return `${dups.length} of these songs are already queued or played recently`
}

function isOrWas(where: string) {
  return where.startsWith('played') ? where : `is ${where}`
}
