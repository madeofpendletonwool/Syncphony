import type { CSSProperties } from 'react'

/**
 * Inline style that exposes a user's lane color as --lane, for classes like
 * `bg-(--lane)`, `text-(--lane)` and `ring-(--lane)/40`.
 */
export function laneStyle(color: string | undefined): CSSProperties | undefined {
  return color ? ({ '--lane': color } as CSSProperties) : undefined
}

/** Up to two initials from a display name: "Ada Lovelace" → "AL". */
export function initials(name: string) {
  const words = name.trim().split(/\s+/).filter(Boolean)
  if (words.length === 0) return '?'
  const first = Array.from(words[0])[0] ?? ''
  const last = words.length > 1 ? (Array.from(words[words.length - 1])[0] ?? '') : ''
  return (first + last).toUpperCase()
}

/** Lane colors the server assigns at signup (server/internal/auth). */
export const LANE_PALETTE = [
  '#7c3aed', '#0ea5e9', '#f97316', '#10b981', '#e11d48', '#eab308',
  '#6366f1', '#14b8a6', '#ec4899', '#84cc16', '#f43f5e', '#06b6d4',
] as const
