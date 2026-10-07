import { act, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { resolveSource, saveSearchSource, useSavedSearchSource } from './search-source'

describe('resolveSource', () => {
  const links = ['nav', 'spot']

  it('prefers the URL over the saved pick', () => {
    expect(resolveSource('spot', 'nav', links)).toBe('spot')
    expect(resolveSource(undefined, 'nav', links)).toBe('nav')
  })

  it('shows everything without a pick', () => {
    expect(resolveSource(undefined, undefined, links)).toBeUndefined()
  })

  it('shows everything when the pick is no longer searchable', () => {
    expect(resolveSource('gone', undefined, links)).toBeUndefined()
    expect(resolveSource(undefined, 'gone', links)).toBeUndefined()
  })

  it('shows everything with a single link', () => {
    expect(resolveSource('nav', undefined, ['nav'])).toBeUndefined()
  })

  it('trusts the pick until the links load', () => {
    expect(resolveSource(undefined, 'nav', undefined)).toBe('nav')
  })
})

describe('saveSearchSource', () => {
  afterEach(() => saveSearchSource(undefined))

  it('remembers the pick on this device and forgets it for all services', () => {
    const { result } = renderHook(() => useSavedSearchSource())
    act(() => saveSearchSource('nav'))
    expect(result.current).toBe('nav')
    expect(localStorage.getItem('syncphony:search-source')).toBe('nav')
    act(() => saveSearchSource(undefined))
    expect(result.current).toBeUndefined()
    expect(localStorage.getItem('syncphony:search-source')).toBeNull()
  })
})
