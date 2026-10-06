import { describe, expect, it } from 'vitest'
import { becauseLabel } from './suggestions'

describe('becauseLabel', () => {
  const seed = { itemId: 'i1', title: 'Heroes', userId: 'bob' }
  const nameOf = (id: string) => ({ bob: 'Bob' })[id]

  it('says which of your songs it is like', () => {
    expect(becauseLabel(seed, 'mine', 'bob', nameOf)).toBe('Like Heroes')
  })
  it('says whose song it is like, in the group vibe', () => {
    expect(becauseLabel(seed, 'group', 'alice', nameOf)).toBe("Like Heroes, Bob's pick")
    expect(becauseLabel(seed, 'group', 'bob', nameOf)).toBe('Like Heroes, your pick')
  })
  it("leaves out a name it doesn't know", () => {
    expect(becauseLabel({ ...seed, userId: 'gone' }, 'group', 'alice', nameOf)).toBe('Like Heroes')
  })
})
