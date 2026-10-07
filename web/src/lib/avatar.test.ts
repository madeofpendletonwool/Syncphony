import { describe, expect, it } from 'vitest'
import { avatarIcon, avatarKind, gravatarUrl, sha256 } from './avatar'

describe('sha256', () => {
  it('matches known digests', () => {
    expect(sha256('')).toBe('e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855')
    expect(sha256('abc')).toBe('ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad')
    // Spans two blocks.
    expect(sha256('a'.repeat(64))).toBe('ffe054fe7ae0cb6dc65c3af9b61d5209f439851db43d0ba5997337df154668eb')
  })
})

describe('gravatarUrl', () => {
  it('hashes the trimmed, lowercased email', () => {
    expect(gravatarUrl('  Ada@Example.com ')).toBe(gravatarUrl('ada@example.com'))
    expect(gravatarUrl('ada@example.com')).toMatch(/^https:\/\/gravatar\.com\/avatar\/[0-9a-f]{64}\?s=256&d=404$/)
  })
})

describe('avatarKind', () => {
  it('tells each kind apart', () => {
    expect(avatarKind(undefined)).toBe('initials')
    expect(avatarKind('icon:cat')).toBe('icon')
    expect(avatarKind('/api/users/u1/avatar?v=1')).toBe('photo')
    expect(avatarKind(gravatarUrl('ada@example.com'))).toBe('gravatar')
    expect(avatarKind('https://www.gravatar.com/avatar/abc')).toBe('gravatar')
    expect(avatarKind('https://example.com/me.png')).toBe('link')
  })
})

describe('avatarIcon', () => {
  it('finds known icons only', () => {
    expect(avatarIcon('icon:cat')).toBeDefined()
    expect(avatarIcon('icon:nope')).toBeUndefined()
    expect(avatarIcon('https://example.com/cat.png')).toBeUndefined()
  })
})
