import { describe, expect, it, vi } from 'vitest'
import { deviceDetail, deviceKind, onDeviceCommand, runDeviceCommand, type RoomDevice } from './devices'

const IPHONE = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148'
const ANDROID = 'Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 Chrome/130.0 Mobile Safari/537.36'
const MAC = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15'
const WINDOWS = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/130.0 Safari/537.36'

describe('deviceKind', () => {
  it('calls phones and tablets phones: they need a tap before they play', () => {
    expect(deviceKind(IPHONE, 5)).toBe('phone')
    expect(deviceKind(ANDROID, 5)).toBe('phone')
    // An iPad says it's a Mac; its touch screen gives it away.
    expect(deviceKind(MAC, 5)).toBe('phone')
  })

  it('calls desktop browsers computers', () => {
    expect(deviceKind(MAC, 0)).toBe('computer')
    expect(deviceKind(WINDOWS, 0)).toBe('computer')
  })
})

describe('deviceDetail', () => {
  const d = (over: Partial<RoomDevice> = {}): RoomDevice => ({
    id: 'd1', kind: 'computer', name: 'Mac', online: true, canSpeak: false, canStop: false, userId: 'u1', userName: 'Collin Pendleton', ...over,
  })

  it('says whose it is, and whether it plays', () => {
    expect(deviceDetail(d(), false, false)).toBe('Collin')
    expect(deviceDetail(d(), false, true)).toBe('Collin · the speaker')
    expect(deviceDetail(d(), true, false)).toBe('This device')
  })

  it('names a paired screen as one, offline or not', () => {
    const screen = d({ kind: 'screen', displayId: 'd1', userId: undefined, userName: undefined, online: false })
    expect(deviceDetail(screen, false, false)).toBe('Paired screen · offline')
  })
})

describe('device commands', () => {
  it('go to the newest handler, then the one before when it leaves', () => {
    const app = vi.fn()
    const tv = vi.fn()
    const offApp = onDeviceCommand(app)
    const offTv = onDeviceCommand(tv)
    const cmd = { action: 'speak', deviceId: 'd1', roomId: 'r1' } as const
    runDeviceCommand(cmd)
    expect(tv).toHaveBeenCalledWith(cmd)
    expect(app).not.toHaveBeenCalled()
    offTv()
    runDeviceCommand(cmd)
    expect(app).toHaveBeenCalledTimes(1)
    offApp()
  })
})
