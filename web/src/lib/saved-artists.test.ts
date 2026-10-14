import { describe, expect, it } from 'vitest'
import { arrangeSaved, artistKey, findSaved, nowPlayingArtists, openableRef, type SavedArtist } from './saved-artists'

describe('artistKey', () => {
  // The same cases as the server's TestSimplify: the keys must agree.
  it.each([
    ["Don't Stop Me Now", 'dont stop me now'],
    ['Simon & Garfunkel', 'simon and garfunkel'],
    ['  AC/DC  ', 'ac dc'],
    ['Sigur Rós', 'sigur ros'],
    ['Beyoncé – Halo!', 'beyonce halo'],
  ])('%s → %s', (name, key) => {
    expect(artistKey(name)).toBe(key)
  })
})

describe('findSaved', () => {
  const saved = [{ id: 's1', name: 'Sigur Rós', key: 'sigur ros', refs: [], savedAt: '' }] as SavedArtist[]
  it('finds an artist however it’s spelled', () => {
    expect(findSaved(saved, 'sigur ros')?.id).toBe('s1')
    expect(findSaved(saved, 'Hum')).toBeUndefined()
    expect(findSaved(undefined, 'Sigur Rós')).toBeUndefined()
  })
})

describe('nowPlayingArtists', () => {
  const track = { artists: ['A', 'B'], artistIds: ['a'], linkId: 'l1' }
  it('saves from the room’s song when there is one', () => {
    expect(nowPlayingArtists({ itemId: 'i1', track } as never)).toEqual([
      { name: 'A', itemId: 'i1', artistIndex: 0 },
      { name: 'B', itemId: 'i1', artistIndex: 1 },
    ])
  })
  it('otherwise by name and where it’s from', () => {
    expect(nowPlayingArtists({ track } as never)).toEqual([
      { name: 'A', linkId: 'l1', artistId: 'a' },
      { name: 'B', linkId: 'l1', artistId: undefined },
    ])
  })
})

describe('openableRef', () => {
  it('opens through the newest link you can use', () => {
    const a = { refs: [{ linkId: 'theirs', id: 'x' }, { linkId: 'mine', id: 'ar1' }, { linkId: 'shared', id: 'ar2' }] }
    expect(openableRef(a, [{ id: 'mine' }, { id: 'shared' }])).toEqual({ linkId: 'mine', id: 'ar1' })
    expect(openableRef(a, [])).toBeUndefined()
    expect(openableRef({ refs: [] }, [{ id: 'mine' }])).toBeUndefined()
  })
})

describe('arrangeSaved', () => {
  const at = (name: string, key: string) => ({ id: key, name, key, refs: [], savedAt: '' }) as SavedArtist
  // Most recently saved first, as the server lists them.
  const saved = [at('Sigur Rós', 'sigur ros'), at('Hum', 'hum'), at('Arab Strap', 'arab strap')]
  it('keeps the newest first, or sorts by name', () => {
    expect(arrangeSaved(saved, 'recent').map((a) => a.name)).toEqual(['Sigur Rós', 'Hum', 'Arab Strap'])
    expect(arrangeSaved(saved, 'name').map((a) => a.name)).toEqual(['Arab Strap', 'Hum', 'Sigur Rós'])
  })
  it('filters by name, ignoring accents and case', () => {
    expect(arrangeSaved(saved, 'recent', 'RÓS').map((a) => a.name)).toEqual(['Sigur Rós'])
  })
})
