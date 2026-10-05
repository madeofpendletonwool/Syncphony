import { describe, expect, it } from 'vitest'
import { relativeTime } from './time'

describe('relativeTime', () => {
  const now = Date.parse('2026-10-05T12:00:00Z')
  const fmt = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })

  it.each([
    ['2026-10-12T12:00:00Z', fmt.format(1, 'week')],
    ['2026-10-05T09:00:00Z', fmt.format(-3, 'hour')],
    ['2026-10-05T12:00:20Z', fmt.format(0, 'second')],
    ['2026-10-06T13:00:00Z', fmt.format(1, 'day')],
  ])('%s', (date, want) => {
    expect(relativeTime(date, now)).toBe(want)
  })
})
