import { describe, expect, it } from 'vitest'
import { canQueue, laneTrackOfSong, playlistLength, songArtworkUrl, type PlaylistSong } from './playlists'

const song = (linkId?: string, durationMs = 180_000): PlaylistSong => ({
  id: 's1',
  addedAt: '2026-10-09T20:00:00Z',
  track: { provider: 'navidrome', linkId, trackId: 't1', title: 'Heroes', artists: ['David Bowie'], artistIds: ['a1'], durationMs, explicit: false, artwork: 'art' },
})

describe('canQueue', () => {
  it('needs a link you can use, unless the room lets you borrow', () => {
    expect(canQueue(song('mine'), new Set(['mine']), false)).toBe(true)
    expect(canQueue(song('theirs'), new Set(['mine']), false)).toBe(false)
    expect(canQueue(song('theirs'), new Set(['mine']), true)).toBe(true)
    // An unlinked service can't play, borrowed or not.
    expect(canQueue(song(undefined), new Set(), true)).toBe(false)
  })
})

describe('laneTrackOfSong', () => {
  it('queues from the playlist song', () => {
    const t = laneTrackOfSong(song('mine'), 'mine')
    expect(t.fromPlaylistSongId).toBe('s1')
    expect(t.artists).toEqual([{ name: 'David Bowie', id: 'a1' }])
  })
})

describe('playlistLength', () => {
  it('counts songs and time', () => {
    expect(playlistLength([])).toBe('0 songs')
    expect(playlistLength([song('l')])).toBe('1 song · 3 min')
    expect(playlistLength(Array.from({ length: 25 }, () => song('l')))).toBe('25 songs · 1 hr 15 min')
  })
})

describe('songArtworkUrl', () => {
  it('goes through the playlist', () => {
    expect(songArtworkUrl('p1', song('l'), 120)).toBe('/api/playlists/p1/songs/s1/artwork?size=120')
    expect(songArtworkUrl('p1', song(undefined))).toBeUndefined()
  })
})
