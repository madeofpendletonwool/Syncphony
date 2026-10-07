import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { UserAvatar } from './user-avatar'

describe('UserAvatar', () => {
  afterEach(cleanup)

  it('shows initials on the lane color when there is no picture', () => {
    render(<UserAvatar user={{ displayName: 'Ada Lovelace', color: '#0ea5e9' }} />)
    const avatar = screen.getByRole('img', { name: 'Ada Lovelace' })
    expect(avatar.textContent).toBe('AL')
    expect(avatar.getAttribute('style')).toContain('--lane: #0ea5e9')
  })

  it('draws a chosen icon instead of initials', () => {
    render(<UserAvatar user={{ displayName: 'Ada Lovelace', color: '#0ea5e9', avatar: 'icon:cat' }} />)
    const avatar = screen.getByRole('img', { name: 'Ada Lovelace' })
    expect(avatar.textContent).toBe('')
    expect(avatar.querySelector('svg')).not.toBeNull()
  })

  it('falls back to initials for an icon it does not know', () => {
    render(<UserAvatar user={{ displayName: 'Ada Lovelace', color: '#0ea5e9', avatar: 'icon:nope' }} />)
    expect(screen.getByRole('img', { name: 'Ada Lovelace' }).textContent).toBe('AL')
  })

  it('shows their name in a tooltip', async () => {
    render(<UserAvatar user={{ displayName: 'Ada Lovelace', color: '#0ea5e9' }} />)
    fireEvent.focus(screen.getByRole('img', { name: 'Ada Lovelace' }))
    expect(await screen.findByRole('tooltip')).toHaveProperty('textContent', 'Ada Lovelace')
  })
})
