import { describe, expect, it } from 'vitest'
import { describeInvite, mayInvite, visibility } from './access'

describe('describeInvite', () => {
  const now = new Date(2026, 9, 7, 12).getTime()

  it('counts people for a link anyone can use', () => {
    expect(describeInvite({ uses: 0 }, now)).toBe('0 people joined · until revoked')
    expect(describeInvite({ uses: 1 }, now)).toBe('1 person joined · until revoked')
  })

  it('counts uses against a limit, and says when it ends', () => {
    const expiresAt = new Date(now + 3 * 24 * 3_600_000).toISOString()
    expect(describeInvite({ uses: 0, maxUses: 1, expiresAt }, now)).toBe('0 of 1 used · ends in 3 days')
  })
})

describe('mayInvite', () => {
  const owner = { id: 'o', role: 'member' }
  const member = { id: 'm', role: 'member' }
  const admin = { id: 'a', role: 'admin' }

  it('lets anyone share an unlisted room', () => {
    expect(mayInvite({ visibility: 'unlisted', ownerId: 'o' }, member)).toBe(true)
  })

  it('leaves a private room to its owner and admins', () => {
    const room = { visibility: 'private' as const, ownerId: 'o' }
    expect(mayInvite(room, owner)).toBe(true)
    expect(mayInvite(room, admin)).toBe(true)
    expect(mayInvite(room, member)).toBe(false)
  })

  it('needs no invites for an open room', () => {
    expect(mayInvite({ visibility: 'open', ownerId: 'o' }, owner)).toBe(false)
  })
})

describe('visibility', () => {
  it('labels each level', () => {
    expect(visibility('private').label).toBe('Private')
  })
})
