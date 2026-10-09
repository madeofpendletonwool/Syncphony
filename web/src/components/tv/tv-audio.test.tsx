import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { playbackQuery, type Playback } from '@/lib/playback'
import { TvAudio } from './tv-audio'

// The speaker is the real one's stand-in; the state store is real, so the
// component can subscribe to it.
const speaker = vi.hoisted(() => ({
  active: false,
  keep: vi.fn(() => false),
  start: vi.fn(async () => undefined),
  apply: vi.fn(),
  stopSoon: vi.fn(),
  onState: undefined as ((np: unknown) => void) | undefined,
}))
vi.mock('@/lib/speaker', async () => {
  const { createStore } = await import('@/lib/store')
  return {
    speaker,
    speakerState: createStore({ status: 'off', mode: 'speaker' as const, keepAwake: false }),
  }
})
vi.mock('@/lib/playback', async (importOriginal) => {
  const real = await importOriginal<typeof import('@/lib/playback')>()
  return { ...real, sendCommand: vi.fn(async () => undefined) }
})

const np = (over: Partial<Playback> = {}): Playback =>
  ({ roomId: 'r1', state: 'paused', positionMs: 0, at: '2026-10-09T00:00:00Z', revision: 1, ...over }) as Playback

function renderAudio(playback: Playback | undefined, props: Partial<Parameters<typeof TvAudio>[0]> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  if (playback) client.setQueryData(playbackQuery('r1').queryKey, playback)
  render(
    <QueryClientProvider client={client}>
      <TvAudio roomId="r1" device="tv-1" name="Living room TV" {...props} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  localStorage.clear()
  speaker.start.mockClear()
})

afterEach(cleanup)

describe('TvAudio', () => {
  it('starts the speaker by itself in box mode when nobody is playing', () => {
    renderAudio(np({ state: 'idle' }), { autoStart: true })
    expect(speaker.start).toHaveBeenCalledTimes(1)
    expect(speaker.start).toHaveBeenCalledWith('r1', 'Living room TV', 'tv-1')
  })

  it("doesn't take over in box mode when someone else is playing", () => {
    renderAudio(
      np({ state: 'playing', player: { deviceId: 'bobs-phone', userId: 'bob', name: "Bob's phone", lastSeen: '2026-10-09T00:00:00Z' } }),
      { autoStart: true },
    )
    expect(speaker.start).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Play here instead' })).toBeDefined()
    expect(screen.getByText("Playing on Bob's phone")).toBeDefined()
  })

  it('still waits for a press when it is not a box', () => {
    renderAudio(np({ state: 'idle' }))
    expect(speaker.start).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Play the audio here' })).toBeDefined()
  })

  it('starts without a press after a reload even off a box, when it was playing here', () => {
    localStorage.setItem('syncphony-tv-audio', '1')
    renderAudio(np({ state: 'idle' }))
    expect(speaker.start).toHaveBeenCalledTimes(1)
  })
})
