import { describe, expect, it } from 'vitest'
import { autopilotReason, autopilotSource, isMine } from './autopilot'

describe('isMine', () => {
  it("counts your songs, not autopilot's", () => {
    expect(isMine({ addedBy: 'me' }, 'me')).toBe(true)
    expect(isMine({ addedBy: 'you' }, 'me')).toBe(false)
    expect(isMine({ addedBy: 'me', autopilot: { seedTitle: 'Heroes' } }, 'me')).toBe(false)
  })
})

describe('autopilotReason', () => {
  it('names the seed', () => {
    expect(autopilotReason({ seedItemId: 'i1', seedTitle: 'Heroes', seedArtist: 'David Bowie' })).toBe('Like Heroes')
    expect(autopilotReason({})).toBe('To keep the music going')
  })

  it("prefers the DJ's reason, and credits its source", () => {
    const pick = { seedTitle: 'Creep', reason: "Because Sam's Radiohead → Portishead (similar 0.82)", source: 'Last.fm, Deezer' }
    expect(autopilotReason(pick)).toBe("Because Sam's Radiohead → Portishead (similar 0.82)")
    expect(autopilotSource(pick)).toBe('via Last.fm, Deezer')
    expect(autopilotSource({ seedTitle: 'Creep', source: 'Last.fm' })).toBeUndefined()
    expect(autopilotSource({ reason: 'A top song by Radiohead, an artist the room loves' })).toBeUndefined()
  })
})
