import { createStore, useStore } from './store'

// The account Search is narrowed to stays on this device, like recent
// searches. Storage can be missing or full (private windows), so every read
// and write is best effort.
const KEY = 'syncphony:search-source'

function load(): string | undefined {
  try {
    return localStorage.getItem(KEY) || undefined
  } catch {
    return undefined
  }
}

const saved = createStore<string | undefined>(load())

/** Remembers the link Search shows, or forgets it for all services. */
export function saveSearchSource(linkId: string | undefined) {
  saved.set(linkId)
  try {
    if (linkId) localStorage.setItem(KEY, linkId)
    else localStorage.removeItem(KEY)
  } catch {
    // Kept for this visit only.
  }
}

export function useSavedSearchSource() {
  return useStore(saved)
}

/**
 * Which link Search shows: the URL's pick, else the saved one, while it's
 * still one of several you can search. Otherwise everything (undefined).
 * Until the links load the pick is trusted, so results don't flash
 * unfiltered.
 */
export function resolveSource(from: string | undefined, savedId: string | undefined, linkIds: string[] | undefined) {
  const pick = from ?? savedId
  if (!linkIds) return pick
  return pick && linkIds.length > 1 && linkIds.includes(pick) ? pick : undefined
}
