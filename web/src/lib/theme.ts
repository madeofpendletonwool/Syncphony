import { useSyncExternalStore } from 'react'

// Theme preference. Dark is the default; index.html applies the stored
// choice before first paint so there's no flash.

export type ThemePreference = 'dark' | 'light' | 'system'

const KEY = 'syncphony-theme'
const listeners = new Set<() => void>()
const media = typeof window !== 'undefined' ? window.matchMedia?.('(prefers-color-scheme: light)') : undefined

export function readPreference(): ThemePreference {
  try {
    const v = localStorage.getItem(KEY)
    return v === 'light' || v === 'system' ? v : 'dark'
  } catch {
    return 'dark'
  }
}

export function resolveTheme(pref: ThemePreference, systemPrefersLight: boolean): 'dark' | 'light' {
  if (pref === 'system') return systemPrefersLight ? 'light' : 'dark'
  return pref
}

function apply() {
  const theme = resolveTheme(readPreference(), media?.matches ?? false)
  document.documentElement.classList.toggle('dark', theme === 'dark')
  document.documentElement.style.colorScheme = theme
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', theme === 'dark' ? '#0b0b12' : '#f6f5fa')
}

export function setPreference(pref: ThemePreference) {
  try {
    localStorage.setItem(KEY, pref)
  } catch {
    // Private mode: the choice lasts for this page load only.
  }
  apply()
  listeners.forEach((l) => l())
}

media?.addEventListener('change', () => {
  apply()
  listeners.forEach((l) => l())
})

export function useThemePreference() {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    readPreference,
    () => 'dark' as const,
  )
}
