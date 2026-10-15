import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { ApiError, unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'

// Big-screen displays (MAD-716): a TV at /tv shows a code; someone in the
// room types it in to pair it. With audio on, it can be the speaker too.

export type Display = components['schemas']['Display']
export type DisplayMe = components['schemas']['DisplayMe']
export type DisplayPairingInfo = components['schemas']['DisplayPairingInfo']

export const displaysQuery = (roomId: string) =>
  queryOptions({
    queryKey: ['displays', roomId],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/displays', { params: { path: { roomId } } })),
  })

/** While someone types in a code: what kind of screen waits behind it, so they pair it with the right defaults. */
export const displayPairingLookupQuery = (roomId: string, code: string) =>
  queryOptions({
    queryKey: ['display-pairing', roomId, code],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/displays/pairing/{code}', { params: { path: { roomId, code } } })),
    staleTime: 60_000,
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

/** A box says what it is, and what it would like to be called (ADR 0016). */
export function beginPairing(about?: components['schemas']['BeginDisplayPairingRequest']) {
  return unwrap(api.POST('/display/pairing', about ? { body: about } : {}))
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

/** Remote care (MAD-811): reloads a screen's page; a box restarts its browser. */
export function reloadDisplay(roomId: string, displayId: string) {
  return unwrap(api.POST('/rooms/{roomId}/displays/{displayId}/reload', { params: { path: { roomId, displayId } } }))
}

/** Reboots a Syncphony box. */
export function restartDisplay(roomId: string, displayId: string) {
  return unwrap(api.POST('/rooms/{roomId}/displays/{displayId}/restart', { params: { path: { roomId, displayId } } }))
}

export type BoxStatus = components['schemas']['BoxStatus']

/** A box warms past this and the Screens list says it runs hot (the Pi's soft limit). */
const HOT_C = 80

/** What's worth warning about on a box, worst first. */
export function boxWarnings(b: BoxStatus): string[] {
  const out: string[] = []
  if (b.throttled || b.underVoltage || (b.temperatureC ?? 0) >= HOT_C) {
    out.push(b.underVoltage ? 'Power supply too weak' : 'Running hot / throttled')
  }
  if (b.update?.result === 'failed') out.push('Update failed')
  if (b.outdated) out.push('Box software older than this server expects')
  return out
}

/** "Syncphony Box 1.3 · online · 52 °C" */
export function boxSummary(b: BoxStatus, online: boolean) {
  const v = b.version.replace(/^v/, '').replace(/-.*$/, '').replace(/\.0$/, '')
  const parts = [`Syncphony Box ${v}`, online ? 'online' : 'offline']
  if (online && b.temperatureC !== undefined) parts.push(`${Math.round(b.temperatureC)} °C`)
  return parts.join(' · ')
}
