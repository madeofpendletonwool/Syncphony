import { describe, expect, it } from 'vitest'
import { suggestUsername, usernameProblem, USERNAME_PATTERN } from './username'

describe('suggestUsername', () => {
  it.each([
    ['Ada Lovelace', 'ada.lovelace'],
    ['Zoë Q. Smith', 'zoe.q.smith'],
    ['  DJ   Snake! ', 'dj.snake'],
    ['mc_hammer-2', 'mc.hammer.2'],
    ['🎧', ''],
    ['a'.repeat(40), 'a'.repeat(32)],
  ])('%s → %s', (name, want) => {
    expect(suggestUsername(name)).toBe(want)
  })

  it('suggests only valid usernames when long enough', () => {
    for (const name of ['Ada Lovelace', 'Zoë Q. Smith', 'x'.repeat(31) + ' y']) {
      expect(suggestUsername(name)).toMatch(USERNAME_PATTERN)
    }
  })
})

describe('usernameProblem', () => {
  it('accepts valid usernames', () => {
    expect(usernameProblem('ada.l_2-x')).toBeUndefined()
  })
  it.each(['a', 'Ada', 'ada lovelace', 'ada!', 'a'.repeat(33)])('rejects %s', (u) => {
    expect(usernameProblem(u)).toBeDefined()
  })
})
