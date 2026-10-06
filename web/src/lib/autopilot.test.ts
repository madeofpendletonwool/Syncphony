import { describe, expect, it } from 'vitest'
import { autopilotReason, isMine } from './autopilot'

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
})
