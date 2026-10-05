import { api } from '@/api/client'
import { ApiError, errorMessage, unwrap } from '@/api/errors'
import { queueArtworkUrl, sendCommand, type Playback, type QueueItem } from './playback'
import { createStore } from './store'
import { toast } from './toast'

// Player mode: this device plays the room's audio (ADR 0003). The server
// decides what plays; the speaker applies each state revision once and
// reports back what the audio element actually did.
//
// Audio goes through plain <audio> elements, not Web Audio: mobile browsers
// suspend AudioContexts in the background but keep media elements playing.
// Two elements take turns, so the next song is already buffered when the
// current one ends.

export type SpeakerStatus =
  | 'off'
  /** Claimed; waiting for a song or for the stream to start. */
  | 'waiting'
  | 'playing'
  | 'paused'
  /** The song plays on its own service's device (a remote provider). */
  | 'remote'
  /** The browser blocked audio until someone taps. */
  | 'blocked'

export type SpeakerState = {
  status: SpeakerStatus
  roomId?: string
  keepAwake: boolean
}

const DEVICE_KEY = 'syncphony-device'
const AWAKE_KEY = 'syncphony-keep-awake'
// How often to tell the server where we are while playing.
const PROGRESS_EVERY = 5_000
// Resync if the audio drifts this far from the server's position.
const MAX_DRIFT = 2_000
// Retries before a failing stream is reported as an error.
const RETRIES = 2

export const speakerState = createStore<SpeakerState>({ status: 'off', keepAwake: readFlag(AWAKE_KEY, true) })

/** A stable ID for this browser, so the server knows it's the same speaker. */
export function deviceId() {
  try {
    let id = localStorage.getItem(DEVICE_KEY)
    if (!id) {
      id = crypto.randomUUID()
      localStorage.setItem(DEVICE_KEY, id)
    }
    return id
  } catch {
    return (fallbackId ??= crypto.randomUUID())
  }
}
let fallbackId: string | undefined

/** Content types this browser can decode, for the stream's `accept`. */
export function acceptedTypes(probe: (type: string) => string = (t) => new Audio().canPlayType(t)) {
  return ['audio/mpeg', 'audio/aac', 'audio/mp4', 'audio/flac', 'audio/ogg', 'audio/wav', 'audio/webm'].filter(
    (t) => probe(t) !== '',
  )
}

/** Where the speaker should be in a song right now, per the server. */
export function targetPosition(p: Playback, now = Date.now()) {
  if (p.state !== 'playing') return p.positionMs
  return p.positionMs + Math.max(0, now - Date.parse(p.at))
}

class Speaker {
  private els: [HTMLAudioElement, HTMLAudioElement] | undefined
  private cur!: HTMLAudioElement
  private pre!: HTMLAudioElement
  private curItem?: QueueItem
  private preItem?: string
  private roomId?: string
  private rev = -1
  private last?: Playback
  private lastProgress = 0
  private retries = 0
  // Pauses until this time are our own doing (a command, a new song), not
  // something to report.
  private ownPauseUntil = 0
  private reclaiming = false
  private wakeLock?: WakeLockSentinel
  /** Receives every playback state the server returns to a report. */
  onState?: (p: Playback) => void

  get active() {
    return this.roomId !== undefined
  }

  /**
   * Makes this device the room's speaker. Call it from a tap: browsers only
   * let audio start after a user gesture, so the elements are unlocked
   * before anything async happens.
   */
  async start(roomId: string, name: string) {
    const [a, b] = this.elements()
    for (const el of [a, b]) {
      el.src = SILENCE
      el.play().catch(() => {})
    }
    this.roomId = roomId
    this.rev = -1
    this.set({ status: 'waiting', roomId })
    this.mediaHandlers()
    void this.keepAwake(speakerState.get().keepAwake)
    try {
      const np = await unwrap(
        api.PUT('/rooms/{roomId}/player', { params: { path: { roomId } }, body: { deviceId: deviceId(), name } }),
      )
      this.onState?.(np)
      this.apply(np)
    } catch (err) {
      this.halt()
      toast({ message: errorMessage(err), tone: 'error' })
    }
  }

  /** Stops being the speaker. */
  async stop() {
    const roomId = this.roomId
    this.halt()
    if (!roomId) return
    try {
      const np = await unwrap(
        api.DELETE('/rooms/{roomId}/player', { params: { path: { roomId }, query: { deviceId: deviceId() } } }),
      )
      this.onState?.(np)
    } catch (err) {
      // Someone else may already have taken over; nothing to undo.
      if (!(err instanceof ApiError && err.status < 500)) toast({ message: errorMessage(err), tone: 'error' })
    }
  }

  /** After the browser blocked audio: try again from a tap. */
  resume() {
    this.play()
  }

  /** Applies the server's playback state. Safe to call with any update. */
  apply(np: Playback | undefined) {
    if (!np || !this.active || np.roomId !== this.roomId) return
    this.last = np
    if (np.player?.deviceId !== deviceId()) {
      if (np.player) {
        this.halt()
        toast({ message: `${np.player.name} took over as the speaker.` })
      } else {
        // The server forgot us (a restart, or we were released): claim again.
        void this.reclaim()
      }
      return
    }
    this.preload(np)
    this.mediaSession(np)
    if (np.revision <= this.rev) {
      // Already applied. If the server is waiting for us to start a song
      // we started early (gapless handoff), say so now.
      if (np.state === 'loading' && np.item?.id === this.curItem?.id && !this.cur.paused) this.report('playing')
      return
    }
    this.rev = np.revision

    const item = np.item
    if (!item || np.state === 'idle') {
      this.pause()
      this.curItem = undefined
      this.set({ status: 'waiting' })
      return
    }
    if (np.driver === 'remote') {
      this.pause()
      this.curItem = item
      this.set({ status: 'remote' })
      return
    }

    const target = targetPosition(np)
    if (item.id !== this.curItem?.id) {
      this.load(item, target)
    } else if (Math.abs(this.cur.currentTime * 1000 - target) > MAX_DRIFT) {
      this.cur.currentTime = target / 1000
    }
    if (np.state === 'playing' || np.state === 'loading') {
      // Already playing it (we started the preloaded song when the last one
      // ended): no new 'playing' event will come, so tell the server now.
      if (np.state === 'loading' && !this.cur.paused && item.id === this.curItem?.id) this.report('playing')
      this.play()
    } else {
      this.pause()
      this.set({ status: 'paused' })
    }
  }

  setKeepAwake(on: boolean) {
    writeFlag(AWAKE_KEY, on)
    this.set({ keepAwake: on })
    if (this.active) void this.keepAwake(on)
  }

  // --- Audio ----------------------------------------------------------------

  private elements() {
    if (!this.els) {
      const make = () => {
        const el = new Audio()
        el.preload = 'auto'
        el.addEventListener('playing', () => el === this.cur && this.onPlaying())
        el.addEventListener('pause', () => el === this.cur && this.onPause())
        el.addEventListener('timeupdate', () => el === this.cur && this.onTime())
        el.addEventListener('ended', () => el === this.cur && this.onEnded())
        el.addEventListener('error', () => el === this.cur && this.onError())
        return el
      }
      this.els = [make(), make()]
      ;[this.cur, this.pre] = this.els
      window.addEventListener('online', () => this.active && this.cur.error && this.recover())
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible' && this.active) void this.keepAwake(speakerState.get().keepAwake)
      })
    }
    return this.els
  }

  private load(item: QueueItem, positionMs: number) {
    this.retries = 0
    if (this.preItem === item.id) {
      this.expectPause()
      this.cur.pause()
      ;[this.cur, this.pre] = [this.pre, this.cur]
      this.preItem = undefined
    } else {
      this.expectPause()
      this.cur.src = this.streamUrl(item)
    }
    this.curItem = item
    this.seekOnLoad(positionMs)
  }

  private seekOnLoad(positionMs: number) {
    const el = this.cur
    const seek = () => {
      if (Math.abs(el.currentTime * 1000 - positionMs) > 500) el.currentTime = positionMs / 1000
    }
    if (el.readyState >= HTMLMediaElement.HAVE_METADATA) seek()
    else el.addEventListener('loadedmetadata', seek, { once: true })
  }

  private preload(np: Playback) {
    const next = np.next
    if (!next || np.driver === 'remote' || next.id === this.preItem || next.id === this.curItem?.id) return
    this.pre.pause()
    this.pre.src = this.streamUrl(next)
    this.pre.load()
    this.preItem = next.id
  }

  private streamUrl(item: QueueItem) {
    const q = new URLSearchParams({ accept: acceptedTypes().join(',') })
    return `/api/rooms/${encodeURIComponent(this.roomId!)}/stream/${encodeURIComponent(item.id)}?${q}`
  }

  private play() {
    this.cur.play().then(
      () => this.set({ status: 'playing' }),
      (err: unknown) => {
        if (err instanceof DOMException && err.name === 'NotAllowedError') this.set({ status: 'blocked' })
        // AbortError: a newer load interrupted this one; it'll play instead.
      },
    )
  }

  private pause() {
    this.expectPause()
    this.cur.pause()
  }

  private expectPause() {
    this.ownPauseUntil = Date.now() + 1000
  }

  private onPlaying() {
    this.retries = 0
    this.set({ status: 'playing' })
    this.report('playing')
  }

  private onPause() {
    if (Date.now() < this.ownPauseUntil || this.cur.ended || !this.curItem) return
    // Paused by something else: a Bluetooth disconnect, a phone call, the
    // OS. Tell the room.
    this.set({ status: 'paused' })
    this.report('paused')
  }

  private onTime() {
    const now = Date.now()
    if (now - this.lastProgress < PROGRESS_EVERY || this.cur.paused) return
    this.lastProgress = now
    this.report('progress')
    this.positionState()
  }

  private onEnded() {
    const ended = this.curItem
    if (!ended) return
    this.report('ended', ended)
    // Gapless: start the preloaded next song now instead of waiting for the
    // server's round trip. Its new state confirms it.
    const next = this.last?.next
    if (next && this.preItem === next.id) {
      this.load(next, 0)
      this.play()
    }
  }

  private onError() {
    if (!this.curItem || this.cur.src === SILENCE) return
    if (this.retries < RETRIES) {
      this.retries++
      setTimeout(() => this.recover(), 1000 * this.retries)
      return
    }
    const msg = this.cur.error?.message || `media error ${this.cur.error?.code ?? ''}`.trim()
    this.report('error', this.curItem, msg)
  }

  /** Reloads the current song where it left off, after a network blip. */
  private recover() {
    if (!this.curItem) return
    const at = this.cur.currentTime * 1000
    this.cur.src = this.streamUrl(this.curItem)
    this.seekOnLoad(at)
    if (this.last?.state === 'playing' || this.last?.state === 'loading') this.play()
  }

  private halt() {
    this.roomId = undefined
    this.curItem = undefined
    this.preItem = undefined
    this.last = undefined
    this.expectPause()
    for (const el of this.els ?? []) {
      el.pause()
      el.removeAttribute('src')
      el.load()
    }
    if ('mediaSession' in navigator) {
      navigator.mediaSession.metadata = null
      navigator.mediaSession.playbackState = 'none'
    }
    void this.keepAwake(false)
    this.set({ status: 'off', roomId: undefined })
  }

  private async reclaim() {
    if (this.reclaiming || !this.roomId) return
    this.reclaiming = true
    try {
      const np = await unwrap(
        api.PUT('/rooms/{roomId}/player', {
          params: { path: { roomId: this.roomId } },
          body: { deviceId: deviceId(), name: this.last?.player?.name ?? 'Speaker' },
        }),
      )
      this.rev = -1
      this.onState?.(np)
      this.apply(np)
    } catch {
      this.halt()
      toast({ message: 'This device stopped being the speaker.', tone: 'error' })
    } finally {
      this.reclaiming = false
    }
  }

  private report(event: 'playing' | 'progress' | 'paused' | 'ended' | 'error', item = this.curItem, error?: string) {
    const roomId = this.roomId
    if (!roomId || !item) return
    const positionMs = Math.round((item === this.curItem ? this.cur.currentTime : 0) * 1000)
    api
      .POST('/rooms/{roomId}/player/report', {
        params: { path: { roomId } },
        body: { deviceId: deviceId(), itemId: item.id, event, positionMs, error },
      })
      .then(({ data }) => data && this.onState?.(data))
      .catch(() => {
        // The socket will bring us the truth when the network is back.
      })
  }

  // --- Lock screen, Bluetooth buttons, screen -----------------------------------

  private mediaHandlers() {
    if (!('mediaSession' in navigator)) return
    const ms = navigator.mediaSession
    const command = (body: Parameters<typeof sendCommand>[1]) => {
      if (!this.roomId) return
      sendCommand(this.roomId, body).then(
        (np) => this.onState?.(np),
        (err: unknown) => toast({ message: errorMessage(err), tone: 'error' }),
      )
    }
    const set = (action: MediaSessionAction, handler: MediaSessionActionHandler | null) => {
      try {
        ms.setActionHandler(action, handler)
      } catch {
        // Not supported here.
      }
    }
    set('play', () => command({ action: 'play' }))
    set('pause', () => command({ action: 'pause' }))
    set('nexttrack', () => command({ action: 'skip', itemId: this.curItem?.id }))
    set('previoustrack', () => command({ action: 'seek', positionMs: 0 }))
    set('seekto', (d) => d.seekTime !== undefined && command({ action: 'seek', positionMs: Math.round(d.seekTime * 1000) }))
  }

  private mediaSession(np: Playback) {
    if (!('mediaSession' in navigator)) return
    const ms = navigator.mediaSession
    const item = np.item
    if (!item) {
      ms.metadata = null
      ms.playbackState = 'none'
      return
    }
    const art = queueArtworkUrl(np.roomId, item, 512)
    if (ms.metadata?.title !== item.track.title || ms.metadata?.artist !== item.track.artists.join(', ')) {
      ms.metadata = new MediaMetadata({
        title: item.track.title,
        artist: item.track.artists.join(', '),
        album: item.track.album ?? '',
        artwork: art ? [{ src: new URL(art, location.href).href, sizes: '512x512' }] : [],
      })
    }
    ms.playbackState = np.state === 'playing' || np.state === 'loading' ? 'playing' : 'paused'
    this.positionState()
  }

  private positionState() {
    const np = this.last
    if (!('mediaSession' in navigator) || !np?.item) return
    const duration = np.item.track.durationMs / 1000
    const position = Math.min(this.cur?.currentTime ?? 0, duration)
    try {
      if (duration > 0) navigator.mediaSession.setPositionState({ duration, position, playbackRate: 1 })
    } catch {
      // Out-of-range positions throw; skip this update.
    }
  }

  private async keepAwake(on: boolean) {
    if (!on) {
      await this.wakeLock?.release().catch(() => {})
      this.wakeLock = undefined
      return
    }
    if (!('wakeLock' in navigator) || (this.wakeLock && !this.wakeLock.released)) return
    try {
      this.wakeLock = await navigator.wakeLock.request('screen')
    } catch {
      // Denied (battery saver, or the page is hidden); tried again on return.
    }
  }

  private set(patch: Partial<SpeakerState>) {
    speakerState.set((s) => ({ ...s, ...patch }))
  }
}

export const speaker = new Speaker()

function readFlag(key: string, fallback: boolean) {
  try {
    const v = localStorage.getItem(key)
    return v === null ? fallback : v === '1'
  } catch {
    return fallback
  }
}

function writeFlag(key: string, on: boolean) {
  try {
    localStorage.setItem(key, on ? '1' : '0')
  } catch {
    // Private mode: lasts until reload.
  }
}

// A tenth of a second of silent 8 kHz mono WAV, for unlocking audio
// elements during the tap that starts player mode.
const SILENCE = (() => {
  const samples = 800
  const bytes = new Uint8Array(44 + samples)
  const view = new DataView(bytes.buffer)
  const text = (at: number, s: string) => [...s].forEach((c, i) => view.setUint8(at + i, c.charCodeAt(0)))
  text(0, 'RIFF')
  view.setUint32(4, 36 + samples, true)
  text(8, 'WAVEfmt ')
  view.setUint32(16, 16, true)
  view.setUint16(20, 1, true) // PCM
  view.setUint16(22, 1, true) // mono
  view.setUint32(24, 8000, true)
  view.setUint32(28, 8000, true)
  view.setUint16(32, 1, true)
  view.setUint16(34, 8, true)
  text(36, 'data')
  view.setUint32(40, samples, true)
  bytes.fill(128, 44) // 8-bit silence is the midpoint
  return `data:audio/wav;base64,${btoa(String.fromCharCode(...bytes))}`
})()
