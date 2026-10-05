import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { UserAvatar } from './user-avatar'

describe('UserAvatar', () => {
  it('shows initials on the lane color when there is no picture', () => {
    render(<UserAvatar user={{ displayName: 'Ada Lovelace', color: '#0ea5e9' }} />)
    const fallback = screen.getByLabelText('Ada Lovelace')
    expect(fallback.textContent).toBe('AL')
    expect(fallback.closest('[data-slot="avatar"]')?.getAttribute('style')).toContain('--lane: #0ea5e9')
  })
})
