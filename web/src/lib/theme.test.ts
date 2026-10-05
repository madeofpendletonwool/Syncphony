import { describe, expect, it } from 'vitest'
import { readPreference, resolveTheme, setPreference } from './theme'

describe('theme', () => {
  it('defaults to dark', () => {
    localStorage.clear()
    expect(readPreference()).toBe('dark')
  })

  it('resolves system against the OS setting', () => {
    expect(resolveTheme('system', true)).toBe('light')
    expect(resolveTheme('system', false)).toBe('dark')
    expect(resolveTheme('light', false)).toBe('light')
  })

  it('persists and applies the choice', () => {
    setPreference('light')
    expect(readPreference()).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    setPreference('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })
})
