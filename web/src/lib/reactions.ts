import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { createStore } from './store'

// Emoji reactions (MAD-716): phones send them, the big screen floats them up.

export type Reaction = components['schemas']['Reaction']
export type ReactionEmoji = components['schemas']['ReactionEmoji']

export const REACTIONS: ReactionEmoji[] = ['🔥', '❤️', '🙌', '😂', '💃', '🎉', '😮', '👏']

export function sendReaction(roomId: string, emoji: ReactionEmoji) {
  return unwrap(api.POST('/rooms/{roomId}/reactions', { params: { path: { roomId } }, body: { emoji } }))
}

/** Reactions that arrived recently, newest last. The room socket adds them. */
export const reactions = createStore<Reaction[]>([])

export function addReaction(r: Reaction) {
  reactions.set((rs) => [...rs, r].slice(-40))
}
