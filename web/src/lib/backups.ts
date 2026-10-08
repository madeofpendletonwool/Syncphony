import type { components } from '@/api/schema.gen'

export type Backup = components['schemas']['Backup']
export type BackupSchedule = components['schemas']['BackupSchedule']
export type Frequency = BackupSchedule['frequency']

export const frequencies: { value: Frequency; label: string }[] = [
  { value: 'off', label: 'Off' },
  { value: '6h', label: 'Every 6 hours' },
  { value: '12h', label: 'Every 12 hours' },
  { value: 'daily', label: 'Daily' },
  { value: 'weekly', label: 'Weekly' },
]

export const weekdays = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

export const kindLabels: Record<Backup['kind'], string> = {
  scheduled: 'Scheduled',
  manual: 'Made by hand',
  'pre-upgrade': 'Before upgrading',
  'pre-restore': 'Before restoring',
}

/** "3:00", "15:00": an hour of the day, as the server's clock shows it. */
export function formatHour(h: number) {
  return `${h}:00`
}

/** The hours of the day backups are made at: "3:00, 9:00, 15:00 and 21:00". */
export function backupHours(s: BackupSchedule) {
  const step = s.frequency === '6h' ? 6 : s.frequency === '12h' ? 12 : 24
  const hours: number[] = []
  for (let h = s.hour % step; h < 24; h += step) hours.push(h)
  return list(hours.map(formatHour))
}

/** When backups are made, e.g. "Every Sunday at 3:00" or "Off". */
export function describeSchedule(s: BackupSchedule) {
  switch (s.frequency) {
    case 'off':
      return 'Off'
    case 'weekly':
      return `Every ${fullWeekday(s.weekday)} at ${formatHour(s.hour)}`
    case 'daily':
      return `Every day at ${formatHour(s.hour)}`
    default:
      return `Every day at ${backupHours(s)}`
  }
}

/** What the rotation keeps, as a sentence. */
export function describeKeeping(s: BackupSchedule) {
  const parts = [
    s.keepDaily > 0 && `one a day for ${plural(s.keepDaily, 'day')}`,
    s.keepWeekly > 0 && `one a week for ${plural(s.keepWeekly, 'week')}`,
    s.keepMonthly > 0 && `one a month for ${plural(s.keepMonthly, 'month')}`,
  ].filter((p): p is string => !!p)
  if (parts.length === 0) return 'Keeps the newest scheduled backup, and any from the last day.'
  return `Keeps every scheduled backup from the last day, then ${list(parts)}.`
}

function fullWeekday(d: number) {
  return ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'][d] ?? 'Sunday'
}

function plural(n: number, unit: string) {
  return `${n} ${unit}${n === 1 ? '' : 's'}`
}

function list(items: string[]) {
  if (items.length <= 1) return items.join('')
  return `${items.slice(0, -1).join(', ')} and ${items[items.length - 1]}`
}

/** The URL a backup downloads from. */
export function downloadURL(name: string) {
  return `/api/admin/backups/${encodeURIComponent(name)}`
}
