import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { box, boxName, boxVersion, isBox } from './box'

describe('kiosk mode', () => {
  it('knows it when the URL says box', () => {
    expect(isBox('?box=1.3.0')).toBe(true)
    expect(boxVersion('?box=1.3.0')).toBe('1.3.0')
    expect(isBox('?name=Living+room+TV')).toBe(false)
    expect(isBox('')).toBe(false)
    expect(isBox('?box=')).toBe(false)
  })

  it('takes the box name from the URL', () => {
    expect(boxName('?box=1&name=Living%20room%20TV')).toBe('Living room TV')
    expect(boxName('?box=1')).toBeUndefined()
  })
})

describe('BoxBridge', () => {
  const fetchMock = vi.fn()

  beforeEach(() => {
    vi.useFakeTimers()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })
  afterEach(() => {
    box.stop()
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  // What was posted where: [url, parsed body].
  const calls = () =>
    fetchMock.mock.calls.map(([url, init]) => [String(url), (init as RequestInit | undefined)?.body ? JSON.parse(String((init as RequestInit).body)) : undefined] as const)
  const urls = (suffix: string) => calls().filter(([url]) => url.endsWith(suffix))

  it('probes info, heartbeats every 30 s, and keeps probing until boxd answers', async () => {
    fetchMock.mockImplementation(async (url) =>
      String(url).endsWith('/v1/info')
        ? new Response(JSON.stringify({ version: '1.3.0', name: 'Living room TV', capabilities: ['cec', 'reboot'] }), { status: 200 })
        : new Response(null, { status: 202 }),
    )
    box.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(box.info).toEqual({ version: '1.3.0', name: 'Living room TV', capabilities: ['cec', 'reboot'] })
    await vi.advanceTimersByTimeAsync(60_000)
    expect(urls('/v1/info')).toHaveLength(1)
    expect(urls('/v1/heartbeat')).toHaveLength(3)
  })

  it('sends each playback change once, and the events carry the room', () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 202 }))
    box.start()
    box.playback('r1')
    box.playback('r1', { state: 'playing', item: { id: 'i1' } })
    box.playback('r1', { state: 'playing', item: { id: 'i1' } })
    box.playback('r1', { state: 'paused', item: { id: 'i1' } })
    box.paired('r1')
    box.paired('r1')
    box.unpaired()
    expect(urls('/v1/events').map(([, body]) => body)).toEqual([
      { type: 'idle', roomId: 'r1' },
      { type: 'playing', roomId: 'r1' },
      { type: 'paused', roomId: 'r1' },
      { type: 'paired', roomId: 'r1' },
      { type: 'unpaired' },
    ])
  })

  it('keeps going when boxd is missing: every call is optional', async () => {
    fetchMock.mockRejectedValue(new Error('connection refused'))
    box.start()
    box.paired('r1')
    box.playback('r1', { state: 'playing', item: { id: 'i1' } })
    await vi.advanceTimersByTimeAsync(30_000)
    expect(calls().length).toBeGreaterThan(0)
    expect(box.info).toBeUndefined()
  })

  it('stays quiet once stopped', () => {
    box.start()
    box.stop()
    fetchMock.mockClear()
    box.playback('r1', { state: 'playing', item: { id: 'i1' } })
    box.paired('r1')
    vi.advanceTimersByTime(120_000)
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
