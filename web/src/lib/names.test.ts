import { describe, expect, it } from 'vitest'
import { ApiError } from '@/api/errors'
import { albumTarget, artistTarget, directLink, trackCredits } from './names'

describe('trackCredits', () => {
  it('pairs each artist with its ID, leaving out the ones the service didn’t give', () => {
    expect(trackCredits({ artists: ['A', 'B', 'C'], artistIds: ['a', ''] })).toEqual([
      { name: 'A', id: 'a' },
      { name: 'B', id: undefined },
      { name: 'C', id: undefined },
    ])
    expect(trackCredits({ artists: ['A'] })).toEqual([{ name: 'A', id: undefined }])
  })
})

describe('directLink', () => {
  const usable = [{ id: 'mine' }, { id: 'shared' }]
  it('opens through a link you can use', () => {
    expect(directLink('shared', 'ar1', usable)).toBe('shared')
  })
  it('looks the name up for someone else’s link, a missing ID, or before links load', () => {
    expect(directLink('theirs', 'ar1', usable)).toBeUndefined()
    expect(directLink('mine', undefined, usable)).toBeUndefined()
    expect(directLink(undefined, 'ar1', usable)).toBeUndefined()
    expect(directLink('mine', 'ar1', undefined)).toBeUndefined()
  })
})

describe('artistTarget and albumTarget', () => {
  it('open the page a service has', async () => {
    expect(await artistTarget('Null Island', async () => ({ linkId: 'l1', id: 'ar2' }))).toEqual({
      to: '/artist/$linkId/$artistId',
      params: { linkId: 'l1', artistId: 'ar2' },
    })
    expect(await albumTarget('Zero Zero', async () => ({ linkId: 'l1', id: 'al3' }))).toEqual({
      to: '/album/$linkId/$albumId',
      params: { linkId: 'l1', albumId: 'al3' },
    })
  })
  it('search for the name when no service has it', async () => {
    const missing = async () => {
      throw new ApiError(404, 'not_found', 'not found')
    }
    expect(await artistTarget('Null Island', missing)).toEqual({ to: '/search', search: { q: 'Null Island', tab: 'artists' } })
    expect(await albumTarget('Zero Zero', missing)).toEqual({ to: '/search', search: { q: 'Zero Zero', tab: 'albums' } })
  })
})
