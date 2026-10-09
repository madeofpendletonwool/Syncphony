// The Syncphony box (ADR 0016): a Pi plugged into a TV, running /tv as a
// kiosk. When the URL says so (?box=<version>), the page talks to the
// box's own helper, boxd, on loopback — to tell it when music plays (for
// CEC) and that the page is healthy (the watchdog). Every call is
// optional: without boxd, this is just a normal /tv. All the box-specific
// code lives here; the rest of /tv only checks isBox().

const BOX_URL = 'http://127.0.0.1:8099'
const HEARTBEAT_EVERY = 30_000

export type BoxEvent = 'playing' | 'paused' | 'idle' | 'paired' | 'unpaired'

/** What boxd says about the box, once it answered /v1/info. */
export type BoxInfo = {
  version: string
  name: string
  capabilities: string[]
}

/** The box software's version, from ?box= — present means this page runs on a box. */
export function boxVersion(search = location.search) {
  const v = new URLSearchParams(search).get('box')
  return v === null || v === '' ? undefined : v
}

/** Whether this page runs on a box (ADR 0016 kiosk mode). */
export function isBox(search = location.search) {
  return boxVersion(search) !== undefined
}

/** What the box calls itself (?name=, from its config file), if it says. */
export function boxName(search = location.search) {
  const n = new URLSearchParams(search).get('name')
  return n === null || n === '' ? undefined : n
}

class BoxBridge {
  private timer?: ReturnType<typeof setInterval>
  // The last event sent, so repeats (re-renders, StrictMode) stay quiet.
  private last?: { type: BoxEvent; roomId?: string }
  /** What boxd said about itself, once it answered. */
  info?: BoxInfo

  /** Starts talking to the box: probes /v1/info and heartbeats while the page is healthy. */
  start() {
    if (this.timer) return
    this.timer = setInterval(() => {
      this.beat()
      // boxd may come up after the page: keep asking until it answers.
      if (!this.info) void this.probe()
    }, HEARTBEAT_EVERY)
    void this.probe()
    this.beat()
  }

  stop() {
    clearInterval(this.timer)
    this.timer = undefined
    this.info = undefined
    this.last = undefined
  }

  /** The room's playback changed: what the box uses for CEC and health. */
  playback(roomId: string, np?: { state?: string; item?: unknown }) {
    const type = !np || !np.item || np.state === 'idle' ? 'idle' : np.state === 'paused' ? 'paused' : 'playing'
    this.event(type, roomId)
  }

  /** This display became paired with a room, or stopped being one. */
  paired(roomId: string) {
    this.event('paired', roomId)
  }

  unpaired() {
    this.event('unpaired')
  }

  /** Remote care (ADR 0016, stage 4): reboot the box, or reload its page. */
  async reboot() {
    await this.post('/v1/reboot')
  }

  async reload() {
    await this.post('/v1/reload')
  }

  private event(type: BoxEvent, roomId?: string) {
    if (!this.timer) return
    if (this.last?.type === type && this.last.roomId === roomId) return
    this.last = { type, roomId }
    void this.post('/v1/events', { type, roomId })
  }

  private beat() {
    if (!this.timer) return
    void this.post('/v1/heartbeat')
  }

  private async probe() {
    try {
      const r = await fetch(BOX_URL + '/v1/info')
      if (r.ok) this.info = (await r.json()) as BoxInfo
    } catch {
      // No boxd, or it's busy: a normal /tv.
    }
  }

  private async post(path: string, body?: object) {
    try {
      await fetch(BOX_URL + path, {
        method: 'POST',
        headers: body ? { 'Content-Type': 'application/json' } : undefined,
        body: body ? JSON.stringify(body) : undefined,
      })
    } catch {
      // boxd is allowed to be missing.
    }
  }
}

export const box = new BoxBridge()
