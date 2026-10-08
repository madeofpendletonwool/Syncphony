import { beatSettings, beatSource } from '@/lib/beat'
import { useStore } from '@/lib/store'

export const SCENES = ['mesh', 'horizon', 'ripples', 'aurora', 'embers'] as const
export type Scene = (typeof SCENES)[number]

// Picks follow the song whose beat map has settled (mapKey), not the one
// that just started: while the new song's map is on its way the old pick
// stays, rather than picking now and again when the map lands (two
// crossfades in a row read as a flicker).

/**
 * The backdrop scene for the song playing: the setting's, or one that suits
 * the song, from its beat map. A stable pick by the queue item breaks ties,
 * so two songs alike don't always look the same.
 */
export function useScene(): Scene {
  const { scene } = useStore(beatSettings)
  const { np, map, mapKey } = useStore(beatSource)
  if (scene !== 'auto') return scene as Scene
  const key = mapKey ?? np?.itemId ?? np?.track.trackId
  if (!key) return 'mesh'
  let h = 0
  for (const ch of key) h = (h * 31 + ch.charCodeAt(0)) | 0
  const pick = (...from: Scene[]) => from[Math.abs(h) % from.length]
  if (!map) return pick(...SCENES)
  const { energy, brightness } = map.features
  // No steady beat (ambient, a drone, spoken word): something that flows.
  if (map.bpm === 0) return pick('mesh', 'aurora')
  // Quiet and slow: a ballad, acoustic.
  if (energy < 0.4 || map.bpm < 90) return pick('mesh', 'aurora', 'embers')
  // Loud with a driving beat: dance, rock.
  if (energy > 0.65 && map.bpm >= 110) return brightness > 0.5 ? pick('horizon', 'ripples') : pick('horizon', 'embers')
  return pick(...SCENES)
}

/** The big visualizer's shows (MAD-779, MAD-780). */
export const SHOWS = ['spectrum', 'fluid', 'tunnel', 'horizon', 'rain', 'sparks', 'aurora'] as const
export type Show = (typeof SHOWS)[number]

export function isShow(s: string): s is Show {
  return (SHOWS as readonly string[]).includes(s)
}

/**
 * The visualizer's show: `setting` if it names one, else one that suits
 * the song playing, like the backdrop's pick.
 */
export function useShow(setting: string): Show {
  const { np, map, mapKey } = useStore(beatSource)
  if (isShow(setting)) return setting
  const key = mapKey ?? np?.itemId ?? np?.track.trackId
  if (!key) return 'fluid'
  let h = 7
  for (const ch of key) h = (h * 33 + ch.charCodeAt(0)) | 0
  const pick = (...from: Show[]) => from[Math.abs(h) % from.length]
  if (!map) return pick('spectrum', 'fluid', 'tunnel')
  const { energy } = map.features
  if (map.bpm === 0) return pick('fluid', 'aurora')
  if (energy < 0.4 || map.bpm < 90) return pick('fluid', 'aurora', 'rain')
  if (energy > 0.65 && map.bpm >= 110) return pick('spectrum', 'tunnel', 'horizon', 'sparks')
  return pick(...SHOWS)
}
