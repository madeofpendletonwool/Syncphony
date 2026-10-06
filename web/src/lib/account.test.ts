import { describe, expect, it } from 'vitest'
import { describeDevice } from './account'

describe('describeDevice', () => {
  it.each([
    [
      'Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/19.0 Mobile/15E148 Safari/604.1',
      'Safari on iPhone',
      'phone',
    ],
    [
      'Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148',
      'Safari on iPhone',
      'phone',
    ],
    [
      'Mozilla/5.0 (Linux; Android 16; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Mobile Safari/537.36',
      'Chrome on Android',
      'phone',
    ],
    [
      'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.0.0',
      'Edge on Mac',
      'computer',
    ],
    ['Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:143.0) Gecko/20100101 Firefox/143.0', 'Firefox on Windows', 'computer'],
    [
      'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/19.0 Safari/605.1.15',
      'Safari on Mac',
      'computer',
    ],
  ])('%s', (ua, label, kind) => {
    expect(describeDevice(ua)).toEqual({ label, kind })
  })

  it('falls back for unknown agents', () => {
    expect(describeDevice('')).toEqual({ label: 'Unknown device', kind: 'unknown' })
    expect(describeDevice('curl/8.7.1')).toEqual({ label: 'Unknown device', kind: 'unknown' })
  })
})
