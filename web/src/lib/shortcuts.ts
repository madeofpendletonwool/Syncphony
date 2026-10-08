/** What a key does anywhere in the app (MAD-735). */
export type Shortcut = 'playPause' | 'skip' | 'search' | 'lyrics' | 'help'

/** The cheat sheet's rows, in the order it lists them. */
export const SHORTCUTS: { id: Shortcut; keys: string[][]; label: string }[] = [
  { id: 'playPause', keys: [['Space'], ['K']], label: 'Play or pause' },
  { id: 'skip', keys: [['N']], label: 'Skip, or vote to skip' },
  { id: 'search', keys: [['/'], ['⌘', 'K']], label: 'Search' },
  { id: 'lyrics', keys: [['L']], label: 'Show or hide lyrics' },
  { id: 'help', keys: [['?']], label: 'These shortcuts' },
]

/** In search, while the box has focus. */
export const SEARCH_KEYS: { keys: string[][]; label: string }[] = [
  { keys: [['↓'], ['↑']], label: 'Highlight a song' },
  { keys: [['Enter']], label: 'Add the highlighted song' },
]

// Inputs you don't type into.
const NOT_TEXT = new Set(['button', 'checkbox', 'color', 'file', 'image', 'radio', 'range', 'reset', 'submit'])

/** Whether keys pressed on `el` are someone typing. */
export function isTyping(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false
  if (el.isContentEditable || el.closest('[contenteditable]:not([contenteditable="false"])')) return true
  if (el instanceof HTMLTextAreaElement || el instanceof HTMLSelectElement) return true
  return el instanceof HTMLInputElement && !NOT_TEXT.has(el.type)
}

// Space presses these already; a shortcut on top would press twice.
const PRESSABLE = 'button, a[href], summary, input, [role="button"], [role="switch"], [role="checkbox"], [role="tab"], [role="radio"], [role="menuitem"], [role="option"]'

type Key = Pick<KeyboardEvent, 'key' | 'metaKey' | 'ctrlKey' | 'altKey' | 'repeat' | 'defaultPrevented' | 'target'>

/** The shortcut a key press means, if any. Never while typing. */
export function shortcutOf(e: Key): Shortcut | undefined {
  if (e.defaultPrevented || isTyping(e.target)) return undefined
  if ((e.metaKey || e.ctrlKey) && !e.altKey && e.key.toLowerCase() === 'k') return 'search'
  if (e.metaKey || e.ctrlKey || e.altKey) return undefined
  switch (e.key) {
    case ' ':
      if (e.repeat || (e.target instanceof Element && e.target.closest(PRESSABLE))) return undefined
      return 'playPause'
    // K plays and pauses wherever focus is, even on a button, like YouTube.
    case 'k':
    case 'K':
      return e.repeat ? undefined : 'playPause'
    case 'n':
    case 'N':
      return e.repeat ? undefined : 'skip'
    case '/':
      return 'search'
    case 'l':
    case 'L':
      return e.repeat ? undefined : 'lyrics'
    case '?':
      return 'help'
  }
  return undefined
}

/** What a key (or a remote's button) does on the big screen. */
export type TvKey = 'playPause' | 'skip' | 'nextLook' | 'previousLook'

/**
 * The big screen's keys: space (or K, even on a button) plays and pauses, → skips (or votes to),
 * ↑ and ↓ change the visuals. A remote's D-pad sends arrows too, so → is
 * left to move between buttons while one has focus; nothing sits above or
 * below them, so ↑ and ↓ are always free.
 */
export function tvKeyOf(e: Key): TvKey | undefined {
  if (e.defaultPrevented || isTyping(e.target) || e.metaKey || e.ctrlKey || e.altKey) return undefined
  const onControl = e.target instanceof Element && !!e.target.closest(PRESSABLE)
  switch (e.key) {
    case ' ':
      return e.repeat || onControl ? undefined : 'playPause'
    case 'k':
    case 'K':
      return e.repeat ? undefined : 'playPause'
    case 'ArrowRight':
      return e.repeat || onControl ? undefined : 'skip'
    case 'ArrowUp':
      return 'previousLook'
    case 'ArrowDown':
      return 'nextLook'
  }
  return undefined
}

/** The entry `by` steps from `current` in `list`, wrapping around. */
export function cycle<T>(list: readonly T[], current: T, by: number): T {
  const i = Math.max(0, list.indexOf(current))
  return list[(((i + by) % list.length) + list.length) % list.length]
}
