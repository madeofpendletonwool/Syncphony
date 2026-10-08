import { useCallback } from 'react'
import { attachBeat } from '@/lib/beat'

/**
 * A ref that lets the beat engine drive an element's --beat, --downbeat,
 * --energy and --b0..--b3 (see lib/beat.ts). With `drift`, its CSS
 * animations also follow the song's energy.
 */
export function useBeat<T extends HTMLElement>({ drift = false }: { drift?: boolean } = {}) {
  return useCallback((el: T | null) => (el ? attachBeat(el, { drift }) : undefined), [drift])
}
