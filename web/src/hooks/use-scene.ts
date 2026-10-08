import { beatSettings } from '@/lib/beat'
import { usePlayer } from '@/lib/now-playing'
import { useStore } from '@/lib/store'

export const SCENES = ['mesh', 'horizon', 'ripples', 'aurora', 'embers'] as const
export type Scene = (typeof SCENES)[number]

/**
 * The backdrop scene for the song playing: the setting's, or one picked by
 * the song. The real pick will come from its genre, tempo and energy with
 * the beat map; for now it's a stable hash of the queue item.
 */
export function useScene(): Scene {
  const { scene } = useStore(beatSettings)
  const { nowPlaying } = usePlayer()
  if (scene !== 'auto') return scene as Scene
  const key = nowPlaying?.itemId ?? nowPlaying?.track.trackId
  if (!key) return beatSettings.get().freeRun ? 'horizon' : 'mesh'
  let h = 0
  for (const ch of key) h = (h * 31 + ch.charCodeAt(0)) | 0
  return SCENES[Math.abs(h) % SCENES.length]
}
