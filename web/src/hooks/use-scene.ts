import { beatSettings, beatSource } from '@/lib/beat'
import { useStore } from '@/lib/store'

export const SCENES = ['mesh', 'horizon', 'ripples', 'aurora', 'embers'] as const
export type Scene = (typeof SCENES)[number]

/**
 * The backdrop scene for the song playing: the setting's, or one that suits
 * the song, from its beat map. A stable pick by the queue item breaks ties,
 * so two songs alike don't always look the same.
 */
export function useScene(): Scene {
  const { scene } = useStore(beatSettings)
  const { np, map, mapKey } = useStore(beatSource)
  if (scene !== 'auto') return scene as Scene
  const key = np?.itemId ?? np?.track.trackId
  if (!key) return 'mesh'
  let h = 0
  for (const ch of key) h = (h * 31 + ch.charCodeAt(0)) | 0
  const pick = (...from: Scene[]) => from[Math.abs(h) % from.length]
  if (!map || mapKey !== np?.itemId) return pick(...SCENES)
  const { energy, brightness } = map.features
  // No steady beat (ambient, a drone, spoken word): something that flows.
  if (map.bpm === 0) return pick('mesh', 'aurora')
  // Quiet and slow: a ballad, acoustic.
  if (energy < 0.4 || map.bpm < 90) return pick('mesh', 'aurora', 'embers')
  // Loud with a driving beat: dance, rock.
  if (energy > 0.65 && map.bpm >= 110) return brightness > 0.5 ? pick('horizon', 'ripples') : pick('horizon', 'embers')
  return pick(...SCENES)
}
