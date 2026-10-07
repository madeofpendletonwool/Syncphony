import { describe, expect, it } from 'vitest'
import { albumKey, discography, kindOf, memberYears, mergeAlbums, rolesLine, type LinkedAlbum } from './artist'

const album = (id: string, title: string, year?: number, kind?: LinkedAlbum['kind'], linkId = 'l1'): LinkedAlbum => ({
  id,
  title,
  artists: [],
  year,
  kind,
  linkId,
})

describe('discography', () => {
  it('splits albums into shelves by kind, newest first', () => {
    const albums = [
      album('a1', 'First', 1994),
      album('a2', 'Second', 1998),
      album('s1', 'Hit', 1997, 'single'),
      album('e1', 'Extended', 1995),
      album('c1', 'Best Of', 2010, 'compilation'),
    ]
    const shelves = discography(albums, { e1: 'ep', a1: 'live' })
    expect(shelves.map((s) => [s.title, s.albums.map((a) => a.id)])).toEqual([
      ['Albums', ['a2']],
      ['Singles & EPs', ['s1', 'e1']],
      ['Live', ['a1']],
      ['Compilations', ['c1']],
    ])
  })

  it("trusts the service over MusicBrainz, and calls what nobody knows an album", () => {
    expect(kindOf(album('x', 'X', 1, 'single'), { x: 'live' })).toBe('single')
    expect(kindOf(album('x', 'X'), undefined)).toBe('album')
  })
})

describe('mergeAlbums', () => {
  it("adds other services' albums it doesn't have, by title", () => {
    const own = [album('a1', 'OK Computer')]
    const other = [album('b1', 'OK Computer (Deluxe Edition)', 1997, undefined, 'l2'), album('b2', 'Kid A', 2000, undefined, 'l2')]
    const third = [album('c1', 'kid a', 2000, undefined, 'l3')]
    expect(mergeAlbums(own, [other, third]).map((a) => `${a.linkId}:${a.id}`)).toEqual(['l1:a1', 'l2:b2'])
  })

  it('compares titles without edition notes, accents or punctuation', () => {
    expect(albumKey('Déjà Vu - 2021 Remaster')).toBe('deja vu')
    expect(albumKey('Rock & Roll!')).toBe('rock and roll')
  })
})

describe('members', () => {
  it('says when someone was in the band', () => {
    expect(memberYears({ name: 'A', current: false, roles: [], from: 1994, to: 2012 })).toBe('1994–2012')
    expect(memberYears({ name: 'A', current: true, roles: [], from: 1996 })).toBe('Since 1996')
    expect(memberYears({ name: 'A', current: false, roles: [], to: 1999 })).toBe('Until 1999')
    expect(memberYears({ name: 'A', current: true, roles: [] })).toBe('')
  })

  it('lists roles', () => {
    expect(rolesLine(['guitar', 'lead vocals'])).toBe('Guitar, lead vocals')
    expect(rolesLine([])).toBe('')
  })
})
