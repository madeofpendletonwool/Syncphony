import { describe, expect, it } from 'vitest'
import { MAX_RECENT, withSearch } from './recent-searches'

describe('withSearch', () => {
  it('puts the newest first and tidies spaces', () => {
    expect(withSearch(['abba'], '  daft   punk ')).toEqual(['daft punk', 'abba'])
  })

  it('replaces searches it extends or trims, ignoring case', () => {
    expect(withSearch(['tay', 'abba'], 'Taylor')).toEqual(['Taylor', 'abba'])
    expect(withSearch(['taylor swift', 'abba'], 'taylor')).toEqual(['taylor', 'abba'])
    expect(withSearch(['ABBA'], 'abba')).toEqual(['abba'])
  })

  it('ignores blank searches and keeps the list short', () => {
    expect(withSearch(['abba'], '   ')).toEqual(['abba'])
    const many = Array.from({ length: MAX_RECENT }, (_, i) => `q${i}x`)
    expect(withSearch(many, 'new')).toHaveLength(MAX_RECENT)
    expect(withSearch(many, 'new')[0]).toBe('new')
  })
})
