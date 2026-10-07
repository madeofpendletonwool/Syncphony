import { describe, expect, it } from 'vitest'
import { linkNames, sourceName } from './services'

describe('sourceName', () => {
  it('names your own link by its service', () => {
    expect(sourceName('Navidrome', 'Alice Liddell', true)).toBe('Navidrome')
  })
  it("names a shared link by its owner's first name", () => {
    expect(sourceName('Navidrome', 'Sam Vimes', false)).toBe("Sam's Navidrome")
  })
})

describe('linkNames', () => {
  const link = (id: string, name: string) => ({ id, name, accountLabel: `${id} on music.example.com` })

  it('uses the plain name when it is unique', () => {
    const names = linkNames([link('a', 'Navidrome'), link('b', 'Spotify')], (l) => l.name)
    expect([...names.values()]).toEqual(['Navidrome', 'Spotify'])
  })

  it('adds the account to names that would read the same', () => {
    const names = linkNames([link('a', 'Navidrome'), link('b', 'Navidrome'), link('c', "Sam's Navidrome")], (l) => l.name)
    expect(names.get('a')).toBe('Navidrome · a on music.example.com')
    expect(names.get('b')).toBe('Navidrome · b on music.example.com')
    expect(names.get('c')).toBe("Sam's Navidrome")
  })
})
