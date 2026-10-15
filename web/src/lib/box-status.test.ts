import { describe, expect, it } from 'vitest'
import { boxSummary, boxWarnings, type BoxStatus } from './displays'

const base: BoxStatus = { version: 'v1.3.0', capabilities: ['reboot', 'reload'], outdated: false, reportedAt: '2026-10-16T04:00:00Z' }

describe('the Screens list on a box', () => {
  it('sums a box up in one line', () => {
    expect(boxSummary({ ...base, temperatureC: 52.4 }, true)).toBe('Syncphony Box 1.3 · online · 52 °C')
    expect(boxSummary({ ...base, version: 'v1.3.2-4-gabc1234' }, true)).toBe('Syncphony Box 1.3.2 · online')
    // An offline box's last temperature is stale: leave it out.
    expect(boxSummary({ ...base, temperatureC: 52 }, false)).toBe('Syncphony Box 1.3 · offline')
  })

  it('warns about heat, a failed update and old software', () => {
    expect(boxWarnings(base)).toEqual([])
    expect(boxWarnings({ ...base, temperatureC: 81 })).toEqual(['Running hot / throttled'])
    expect(boxWarnings({ ...base, throttled: true })).toEqual(['Running hot / throttled'])
    expect(boxWarnings({ ...base, underVoltage: true })).toEqual(['Power supply too weak'])
    expect(
      boxWarnings({ ...base, outdated: true, update: { at: '2026-10-16T04:03:00Z', result: 'failed', error: 'no network' } }),
    ).toEqual(['Update failed', 'Box software older than this server expects'])
    // A sticky "throttled since boot" alone isn't worth a warning.
    expect(boxWarnings({ ...base, throttleOccurred: true })).toEqual([])
  })
})
