import { createStore, useStore } from './store'

// Recent searches stay on this device. Storage can be missing or full
// (private windows), so every read and write is best effort.
const KEY = 'syncphony:recent-searches'
export const MAX_RECENT = 8

function load(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? '[]') as unknown
    return Array.isArray(v) ? v.filter((s): s is string => typeof s === 'string').slice(0, MAX_RECENT) : []
  } catch {
    return []
  }
}

const recent = createStore<string[]>(load())

function save(list: string[]) {
  recent.set(list)
  try {
    localStorage.setItem(KEY, JSON.stringify(list))
  } catch {
    // Kept for this visit only.
  }
}

/**
 * Puts q at the front of the list. Typing saves each pause, so a search
 * that extends or trims an earlier one ("tay" then "taylor") replaces it.
 */
export function withSearch(list: string[], q: string): string[] {
  const text = q.trim().replace(/\s+/g, ' ')
  if (!text) return list
  const lower = text.toLowerCase()
  const rest = list.filter((s) => {
    const l = s.toLowerCase()
    return !lower.startsWith(l) && !l.startsWith(lower)
  })
  return [text, ...rest].slice(0, MAX_RECENT)
}

export function rememberSearch(q: string) {
  save(withSearch(recent.get(), q))
}

export function forgetSearch(q: string) {
  save(recent.get().filter((s) => s !== q))
}

export function clearSearches() {
  save([])
}

export function useRecentSearches() {
  return useStore(recent)
}
