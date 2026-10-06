import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { ApiError, unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'

// Big-screen displays (MAD-716): a TV at /tv shows a code; someone in the
// room types it in to pair it. With audio on, it can be the speaker too.

export type Display = components['schemas']['Display']
export type DisplayMe = components['schemas']['DisplayMe']

export const displaysQuery = (roomId: string) =>
  queryOptions({
    queryKey: ['displays', roomId],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/displays', { params: { path: { roomId } } })),
  })

/** This device as a paired display, or null if it isn't one. */
export const displayMeQuery = queryOptions({
  queryKey: ['display-me'],
  queryFn: async () => {
    try {
      return await unwrap(api.GET('/display'))
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) return null
      throw e
    }
  },
  staleTime: 60_000,
})

export function pairDisplay(roomId: string, code: string, name?: string, audio = false) {
  return unwrap(api.POST('/rooms/{roomId}/displays', { params: { path: { roomId } }, body: { code, name, audio } }))
}

/** Lets a display play the room's audio, or stops it. */
export function setDisplayAudio(roomId: string, displayId: string, audio: boolean) {
  return unwrap(
    api.PATCH('/rooms/{roomId}/displays/{displayId}', { params: { path: { roomId, displayId } }, body: { audio } }),
  )
}

export function unpairDisplay(roomId: string, displayId: string) {
  return unwrap(api.DELETE('/rooms/{roomId}/displays/{displayId}', { params: { path: { roomId, displayId } } }))
}

export function beginPairing() {
  return unwrap(api.POST('/display/pairing'))
}

export function pollPairing() {
  return unwrap(api.GET('/display/pairing'))
}

export function leaveDisplay() {
  return unwrap(api.DELETE('/display'))
}

/** A pairing code shown in two easy halves: "ABC DEF". */
export function formatPairingCode(code: string) {
  return code.length === 6 ? `${code.slice(0, 3)} ${code.slice(3)}` : code
}
