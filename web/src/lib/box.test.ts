import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { box, boxName, boxVersion, isBox, runDisplayCommand } from './box'

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
    // Once it answered: again every minute, for fresh status.
    expect(urls('/v1/info')).toHaveLength(2)
    expect(urls('/v1/heartbeat')).toHaveLength(3)
  })

  it('tells listeners when the info changes, and only then', async () => {
    let temp = 50
    fetchMock.mockImplementation(async (url) =>
      String(url).endsWith('/v1/info')
        ? new Response(JSON.stringify({ version: '1.3.0', name: 'TV', capabilities: [], temperatureC: temp }), { status: 200 })
        : new Response(null, { status: 202 }),
    )
    const seen: number[] = []
    const off = box.onInfo((i) => seen.push(i.temperatureC ?? 0))
    box.start()
    await vi.advanceTimersByTimeAsync(60_000)
    temp = 61
    await vi.advanceTimersByTimeAsync(60_000)
    off()
    expect(seen).toEqual([50, 61])
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

describe('runDisplayCommand', () => {
  const fetchMock = vi.fn()
  const original = location.search
  beforeEach(() => {
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })
  afterEach(() => {
    history.replaceState(null, '', original || '/')
    vi.unstubAllGlobals()
  })
  const posted = () => fetchMock.mock.calls.map(([url]) => String(url))

  it('reloads a plain screen itself, and never asks a bridge', async () => {
    history.replaceState(null, '', '/tv')
    const reload = vi.fn()
    await runDisplayCommand({ action: 'reload' }, reload)
    await runDisplayCommand({ action: 'restart' }, reload)
    expect(reload).toHaveBeenCalledTimes(1)
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('has a box reload through boxd, and restart reboot it', async () => {
    history.replaceState(null, '', '/tv?box=v1.0.0')
    fetchMock.mockResolvedValue(new Response(null, { status: 202 }))
    const reload = vi.fn()
    await runDisplayCommand({ action: 'reload' }, reload)
    await runDisplayCommand({ action: 'restart' }, reload)
    expect(posted()).toEqual(['http://127.0.0.1:8099/v1/reload', 'http://127.0.0.1:8099/v1/reboot'])
    expect(reload).not.toHaveBeenCalled()
  })

  it('reloads the page when boxd does not take it', async () => {
    history.replaceState(null, '', '/tv?box=v1.0.0')
    fetchMock.mockRejectedValue(new Error('connection refused'))
    const reload = vi.fn()
    await runDisplayCommand({ action: 'reload' }, reload)
    expect(reload).toHaveBeenCalledTimes(1)
  })
})
