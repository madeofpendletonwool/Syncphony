import { queryOptions } from '@tanstack/react-query'
import { Globe, Link2, Lock, type LucideIcon } from 'lucide-react'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { relativeTime } from './time'

// Who can see and join a room (ADR 0011). Open rooms are everyone's on the
// server; unlisted ones are for their members and anyone with an invite
// link; private ones for the members their owner lets in.

export type Visibility = components['schemas']['RoomVisibility']
export type RoomMember = components['schemas']['RoomMember']
export type RoomInvite = components['schemas']['RoomInvite']
export type RoomInvitePreview = components['schemas']['RoomInvitePreview']

export const VISIBILITIES: { id: Visibility; label: string; hint: string; icon: LucideIcon }[] = [
  { id: 'open', label: 'Open', hint: 'Everyone on this server can see it and join', icon: Globe },
  { id: 'unlisted', label: 'Unlisted', hint: 'Only people with the link. Anyone in it can share the link', icon: Link2 },
  { id: 'private', label: 'Private', hint: 'Only the people you let in', icon: Lock },
]

export const visibility = (v: Visibility) => VISIBILITIES.find((x) => x.id === v) ?? VISIBILITIES[0]

/** Who may see and make the room's invite links. */
export function mayInvite(room: { visibility: Visibility; ownerId: string }, me: { id: string; role: string }) {
  if (room.visibility === 'unlisted') return true
  return room.visibility === 'private' && (room.ownerId === me.id || me.role === 'admin')
}

export const membersQuery = (roomId: string) =>
  queryOptions({
    queryKey: ['room-members', roomId],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/members', { params: { path: { roomId } } })),
  })

export const invitesQuery = (roomId: string) =>
  queryOptions({
    queryKey: ['room-invites', roomId],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/invites', { params: { path: { roomId } } })),
  })

export const roomInviteQuery = (code: string) =>
  queryOptions({
    queryKey: ['room-invite', code],
    queryFn: () => unwrap(api.GET('/room-invites/{code}', { params: { path: { code } } })),
    retry: false,
  })

export function addMember(roomId: string, userId: string) {
  return unwrap(api.PUT('/rooms/{roomId}/members/{userId}', { params: { path: { roomId, userId } } }))
}

export function removeMember(roomId: string, userId: string) {
  return unwrap(api.DELETE('/rooms/{roomId}/members/{userId}', { params: { path: { roomId, userId } } }))
}

export function createInvite(roomId: string, body: components['schemas']['CreateRoomInviteRequest']) {
  return unwrap(api.POST('/rooms/{roomId}/invites', { params: { path: { roomId } }, body }))
}

export function revokeInvite(roomId: string, code: string) {
  return unwrap(api.DELETE('/rooms/{roomId}/invites/{code}', { params: { path: { roomId, code } } }))
}

export function redeemInvite(code: string) {
  return unwrap(api.POST('/room-invites/{code}', { params: { path: { code } } }))
}

export function joinAsAdmin(roomId: string) {
  return unwrap(api.POST('/rooms/{roomId}/admin-join', { params: { path: { roomId } } }))
}

/** How long a private room's invite works. */
export const INVITE_LENGTHS = [
  { id: 'day', label: 'A day', until: (now: Date) => new Date(now.getTime() + 24 * 3_600_000) },
  { id: 'week', label: 'A week', until: (now: Date) => new Date(now.getTime() + 7 * 24 * 3_600_000) },
  { id: 'never', label: 'Until revoked', until: () => undefined },
] as const

/** "1 of 1 used · ends in 3 days", for an invite in a list. */
export function describeInvite(inv: Pick<RoomInvite, 'uses' | 'maxUses' | 'expiresAt'>, now = Date.now()) {
  const used = inv.maxUses ? `${inv.uses} of ${inv.maxUses} used` : inv.uses === 1 ? '1 person joined' : `${inv.uses} people joined`
  const ends = inv.expiresAt ? `ends ${relativeTime(inv.expiresAt, now)}` : 'until revoked'
  return `${used} · ${ends}`
}
