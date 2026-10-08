import { describe, expect, it } from 'vitest'
import { backupHours, describeKeeping, describeSchedule, downloadURL, type BackupSchedule } from './backups'

const base: BackupSchedule = { frequency: 'daily', hour: 3, weekday: 0, keepDaily: 7, keepWeekly: 4, keepMonthly: 6 }

describe('describeSchedule', () => {
  it('says when backups are made', () => {
    expect(describeSchedule(base)).toBe('Every day at 3:00')
    expect(describeSchedule({ ...base, frequency: 'weekly', weekday: 5, hour: 0 })).toBe('Every Friday at 0:00')
    expect(describeSchedule({ ...base, frequency: '6h' })).toBe('Every day at 3:00, 9:00, 15:00 and 21:00')
    expect(describeSchedule({ ...base, frequency: 'off' })).toBe('Off')
  })

  it('counts every 12 hours from the hour', () => {
    expect(backupHours({ ...base, frequency: '12h', hour: 20 })).toBe('8:00 and 20:00')
  })
})

describe('describeKeeping', () => {
  it('lists what the rotation keeps', () => {
    expect(describeKeeping(base)).toBe(
      'Keeps every scheduled backup from the last day, then one a day for 7 days, one a week for 4 weeks and one a month for 6 months.',
    )
    expect(describeKeeping({ ...base, keepDaily: 0, keepWeekly: 1, keepMonthly: 0 })).toBe(
      'Keeps every scheduled backup from the last day, then one a week for 1 week.',
    )
    expect(describeKeeping({ ...base, keepDaily: 0, keepWeekly: 0, keepMonthly: 0 })).toBe(
      'Keeps the newest scheduled backup, and any from the last day.',
    )
  })
})

it('escapes download names', () => {
  expect(downloadURL('syncphony-20261006-030000-scheduled.db')).toBe('/api/admin/backups/syncphony-20261006-030000-scheduled.db')
})
