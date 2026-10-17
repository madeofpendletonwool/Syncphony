import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'

// The room's devices: its paired screens and every signed-in browser with
// the room open. From any of them, someone can hand the speaker to a
// screen or another of their own computers, or stop it, without walking
// over. The device itself claims the room when told to (device.command),
// so it's the one that starts the audio. Phones aren't sent the speaker:
// their browsers only play after a tap on them.

export type RoomDevice = components['schemas']['RoomDevice']
export type DeviceCommand = components['schemas']['DeviceCommand']

export const devicesQuery = (roomId: string) =>
  queryOptions({
    queryKey: ['devices', roomId],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/devices', { params: { path: { roomId } } })),
  })

/** Tells a device to become the room's speaker. */
export function makeSpeaker(roomId: string, deviceId: string) {
  return unwrap(api.PUT('/rooms/{roomId}/devices/{deviceId}/speaker', { params: { path: { roomId, deviceId } } }))
}

/** Tells the room's speaker to stop, and lets go of it. */
export function stopSpeaker(roomId: string, deviceId: string) {
  return unwrap(api.DELETE('/rooms/{roomId}/devices/{deviceId}/speaker', { params: { path: { roomId, deviceId } } }))
}

/**
 * What this browser is, for the room's devices. Phones and tablets need a
 * tap on them before they play, so they're never sent the speaker. An iPad
 * says it's a Mac; its touch screen gives it away.
 */
export function deviceKind(ua = navigator.userAgent, touchPoints = navigator.maxTouchPoints): 'phone' | 'computer' {
  if (/iPhone|iPad|iPod|Android|Mobile/.test(ua)) return 'phone'
  if (/Macintosh/.test(ua) && touchPoints > 1) return 'phone'
  return 'computer'
}

type Handler = (cmd: DeviceCommand) => void
const handlers: Handler[] = []

/**
 * Acts on device commands for this browser: whichever screen is up (the
 * app, or the big screen) knows what to call this device as the speaker.
 * The newest handler wins. Returns the way to stop.
 */
export function onDeviceCommand(fn: Handler) {
  handlers.push(fn)
  return () => {
    const i = handlers.lastIndexOf(fn)
    if (i >= 0) handlers.splice(i, 1)
  }
}

/** A device command arrived on the room socket. */
export function runDeviceCommand(cmd: DeviceCommand) {
  handlers.at(-1)?.(cmd)
}

/** Under a device's name: "Collin · the speaker", "Paired screen · offline", "This device". */
export function deviceDetail(d: RoomDevice, mine: boolean, isSpeaker: boolean) {
  const parts = [mine ? 'This device' : d.displayId ? 'Paired screen' : (d.userName?.split(' ')[0] ?? 'Someone')]
  if (isSpeaker) parts.push('the speaker')
  if (!d.online) parts.push('offline')
  return parts.join(' · ')
}
