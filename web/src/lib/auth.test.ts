import { describe, expect, it } from 'vitest'
import { safeRedirect } from './auth'

describe('safeRedirect', () => {
  it.each([
    ['/search?q=x', '/search?q=x'],
    ['/settings/services', '/settings/services'],
    ['https://evil.example', '/room'],
    ['//evil.example', '/room'],
    ['/\\evil.example', '/room'],
    ['room', '/room'],
    [undefined, '/room'],
    [42, '/room'],
  ])('%s → %s', (to, want) => {
    expect(safeRedirect(to)).toBe(want)
  })
})
