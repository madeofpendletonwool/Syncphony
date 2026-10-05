import { createStore } from './store'

export type Toast = {
  id: number
  message: string
  tone?: 'default' | 'error'
  action?: { label: string; onClick: () => void }
}

export const toasts = createStore<Toast[]>([])
let next = 1

/** Shows a short message above the player dock. */
export function toast(t: Omit<Toast, 'id'>, ms = 3500) {
  const id = next++
  // One at a time: a new toast replaces the last.
  toasts.set([{ ...t, id }])
  setTimeout(() => dismiss(id), ms)
  return id
}

export function dismiss(id: number) {
  toasts.set((ts) => ts.filter((t) => t.id !== id))
}
