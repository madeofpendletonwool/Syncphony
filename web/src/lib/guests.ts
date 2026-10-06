import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { ApiError, unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'

// Guests (MAD-720): someone at the hangout scans the room's guest pass and
// adds songs with no account. A guest is a user with `guest` set: their own
// room only, no services or settings, until the pass expires.

export type GuestPass = components['schemas']['GuestPass']
export type Guest = components['schemas']['Guest']
export type GuestInvite = components['schemas']['GuestInvite']

/** Whether someone is a guest rather than a member. */
export const isGuest = (u: { guest?: unknown } | undefined | null) => !!u?.guest

/** The room's current guest pass, or null if it has none. */
export const guestPassQuery = (roomId: string) =>
  queryOptions({
    queryKey: ['guest-pass', roomId],
    queryFn: async () => {
      try {
        return await unwrap(api.GET('/rooms/{roomId}/guest-pass', { params: { path: { roomId } } }))
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return null
        throw e
      }
    },
    // A pass lapses on its own; check back now and then.
    refetchInterval: 5 * 60_000,
  })

export const guestsQuery = (roomId: string) =>
  queryOptions({
    queryKey: ['guests', roomId],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/guests', { params: { path: { roomId } } })),
  })

export function createGuestPass(roomId: string, expiresAt: Date) {
  return unwrap(api.POST('/rooms/{roomId}/guest-pass', { params: { path: { roomId } }, body: { expiresAt: expiresAt.toISOString() } }))
}

export function revokeGuestPass(roomId: string) {
  return unwrap(api.DELETE('/rooms/{roomId}/guest-pass', { params: { path: { roomId } } }))
}

export function kickGuest(roomId: string, userId: string) {
  return unwrap(api.DELETE('/rooms/{roomId}/guests/{userId}', { params: { path: { roomId, userId } } }))
}

export const guestInviteQuery = (token: string) =>
  queryOptions({
    queryKey: ['guest-invite', token],
    queryFn: () => unwrap(api.GET('/guest/{token}', { params: { path: { token } } })),
    retry: false,
  })

export function joinAsGuest(token: string, displayName: string) {
  return unwrap(api.POST('/guest/{token}', { params: { path: { token } }, body: { displayName } }))
}

/**
 * When the night ends: the next 4am. Already in the small hours, at least
 * two more hours.
 */
export function endOfNight(now = new Date()): Date {
  const end = new Date(now)
  end.setHours(4, 0, 0, 0)
  if (end <= now) end.setDate(end.getDate() + 1)
  const soonest = now.getTime() + 2 * 3_600_000
  return end.getTime() < soonest ? new Date(soonest) : end
}

/** How long a guest pass lasts. */
export const PASS_LENGTHS = [
  { id: 'night', label: 'Tonight', until: (now: Date) => endOfNight(now) },
  { id: '2h', label: '2 hours', until: (now: Date) => new Date(now.getTime() + 2 * 3_600_000) },
  { id: '6h', label: '6 hours', until: (now: Date) => new Date(now.getTime() + 6 * 3_600_000) },
] as const
