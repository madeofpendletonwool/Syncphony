import { api } from '@/api/client'
import { ApiError, errorMessage, unwrap } from '@/api/errors'
import { serverNow } from './clock'
import { queueArtworkUrl, sendCommand, type Playback, type QueueItem } from './playback'
import { createStore } from './store'
import { toast } from './toast'

// Player mode: this device plays the room's audio (ADR 0003). The server
// decides what plays; the speaker applies each state revision once and
// reports back what the audio element actually did.
//
// Listen-along mode: this device plays the room too, in time with the
// speaker, for friends who aren't in the room. A listener follows the same
// state but reports nothing; the speaker keeps the room's clock. If the
// speaker goes away, a listener who may be the speaker takes over, so a
// room of remote friends keeps playing.
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

export type SpeakerMode = 'speaker' | 'listener'

export type SpeakerState = {
  status: SpeakerStatus
  /** Whether this device keeps the room's clock or listens along. */
  mode: SpeakerMode
  roomId?: string
  keepAwake: boolean
}

const DEVICE_KEY = 'syncphony-device'
const AWAKE_KEY = 'syncphony-keep-awake'
// How often to tell the server where we are while playing.
const PROGRESS_EVERY = 5_000
// Resync if the audio drifts this far from the server's position.
const MAX_DRIFT = 2_000
// A stream that can't be seeked is reloaded part way only for a drift this
// big: reloading costs a moment of silence.
const MAX_DRIFT_RELOAD = 4_000
// How often a listener checks its place against the server's.
const RESYNC_EVERY = 15_000
// Retries before a failing stream is reported as an error.
const RETRIES = 2

export const speakerState = createStore<SpeakerState>({ status: 'off', mode: 'speaker', keepAwake: readFlag(AWAKE_KEY, true) })

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
export function targetPosition(p: Playback, now = serverNow()) {
  if (p.state !== 'playing') return p.positionMs
  return p.positionMs + Math.max(0, now - Date.parse(p.at))
}

/**
 * Whether a media element can seek to `seconds`. A transcoded stream can't
 * be seeked by bytes; browsers report it with no seekable range, or only
 * the part already downloaded.
 */
export function canSeek(seekable: Pick<TimeRanges, 'length' | 'start' | 'end'>, seconds: number) {
  for (let i = 0; i < seekable.length; i++) {
    if (seconds >= seekable.start(i) && seconds <= seekable.end(i)) return true
  }
  return false
}

/** A song's stream URL. startMs asks for audio from part way in, for streams that can't be seeked. */
export function streamUrl(roomId: string, itemId: string, accept: string[], startMs = 0) {
  const q = new URLSearchParams({ accept: accept.join(',') })
  if (startMs > 0) q.set('start', String(Math.round(startMs)))
  return `/api/rooms/${encodeURIComponent(roomId)}/stream/${encodeURIComponent(itemId)}?${q}`
}

class Speaker {
  private els: [HTMLAudioElement, HTMLAudioElement] | undefined
  private cur!: HTMLAudioElement
  private pre!: HTMLAudioElement
  // Where each element's stream begins in its song, in ms: 0 unless it was
  // loaded part way because it couldn't be seeked.
  private offsets = new WeakMap<HTMLAudioElement, number>()
  private curItem?: QueueItem
  private preItem?: string
  private roomId?: string
  private mode: SpeakerMode = 'speaker'
  // Which device the server knows this speaker as: this browser, or for a
  // paired TV, its display ID.
  private device = ''
  private name = ''
  private rev = -1
  private last?: Playback
  private lastProgress = 0
  private retries = 0
  // Set once the browser refused to start the preloaded element in the
  // background (iOS may): from then on, songs that start while hidden play
  // in the element that just ended, which it does allow.
  private inPlace = false
  // Pauses until this time are our own doing (a command, a new song), not
  // something to report.
  private ownPauseUntil = 0
  private reclaiming = false
  private wakeLock?: WakeLockSentinel
  // A listener that may be the speaker takes over when the speaker leaves.
  private canSpeak = false
  // This speaker took over from listening: if someone else takes over, it
  // goes back to listening instead of stopping.
  private promoted = false
  // A listener paused its own audio (from the lock screen, or a headset).
  // The room plays on; this device stays quiet until it's played again.
  private localPause = false
  private resyncTimer?: ReturnType<typeof setInterval>
  /** Receives every playback state the server returns to a report. */
  onState?: (p: Playback) => void

  get active() {
    return this.roomId !== undefined
  }

  /**
   * Makes this device the room's speaker. Call it from a tap: browsers only
   * let audio start after a user gesture, so the elements are unlocked
   * before anything async happens. A paired TV passes its display ID as
   * the device, since that's who the server makes the speaker.
   */
  async start(roomId: string, name: string, device = deviceId()) {
    this.begin(roomId, 'speaker', name, device)
    try {
      const np = await unwrap(
        api.PUT('/rooms/{roomId}/player', { params: { path: { roomId } }, body: { deviceId: device, name } }),
      )
      this.onState?.(np)
      this.apply(np)
    } catch (err) {
      this.halt()
      toast({ message: errorMessage(err), tone: 'error' })
    }
  }

  /**
   * Plays the room on this device in time with the speaker, without being
   * it. Call it from a tap, like start. If there's no speaker and canSpeak,
   * this device becomes it, so a room of remote friends still plays.
   */
  listen(roomId: string, name: string, np: Playback | undefined, canSpeak: boolean) {
    this.begin(roomId, 'listener', name, deviceId())
    this.canSpeak = canSpeak
    this.resyncTimer = setInterval(() => void this.resync(), RESYNC_EVERY)
    this.apply(np)
  }

  /** Stops playing here: a speaker gives up the room, a listener just goes quiet. */
  async stop() {
    const roomId = this.roomId
    const speaking = this.mode === 'speaker'
    this.halt()
    if (!roomId || !speaking) return
    try {
      const np = await unwrap(
        api.DELETE('/rooms/{roomId}/player', { params: { path: { roomId }, query: { deviceId: this.device } } }),
      )
      this.onState?.(np)
    } catch (err) {
      // Someone else may already have taken over; nothing to undo.
      if (!(err instanceof ApiError && err.status < 500)) toast({ message: errorMessage(err), tone: 'error' })
    }
  }

  /** After the browser blocked audio, or a listener paused itself: try again from a tap. */
  resume() {
    this.localPause = false
    if (this.mode === 'listener' && this.last) {
      this.seekTo(targetPosition(this.last))
      if (this.last.state !== 'playing') return
    }
    this.play()
  }

  /** Applies the server's playback state. Safe to call with any update. */
  apply(np: Playback | undefined) {
    if (!np || !this.active || np.roomId !== this.roomId) return
    const before = this.last
    this.last = np
    if (this.mode === 'speaker' && np.player?.deviceId !== this.device) {
      if (np.player && this.promoted) {
        // Another device took over from us: listen along with it instead.
        this.becomeListener()
      } else if (np.player) {
        this.halt()
        toast({ message: `${np.player.name} took over as the speaker.` })
        return
      } else {
        // The server forgot us (a restart, or we were released): claim again.
        void this.reclaim()
        return
      }
    } else if (this.mode === 'listener') {
      if (np.player?.deviceId === this.device) {
        this.mode = 'speaker'
        this.set({ mode: 'speaker' })
      } else if (!np.player && this.canSpeak) {
        // The speaker left. Take over, and keep the music going if it was.
        const wasPlaying = before?.state === 'playing' || before?.state === 'loading'
        void this.promote(wasPlaying)
      }
    }
    this.preload(np)
    this.mediaSession(np)
    if (np.revision <= this.rev) {
      // Already applied. If the server is waiting for us to start a song
      // we started early (gapless handoff), say so now.
      if (np.state === 'loading' && np.item?.id === this.curItem?.id && !this.cur.paused) this.report('playing')
      // A listener checks it's still in time, and starts a song it loaded
      // and waited on: the speaker starting it ('loading' to 'playing')
      // doesn't bump the revision.
      if (this.mode === 'listener' && np.state === 'playing' && np.item?.id === this.curItem?.id) {
        this.keepTime(np)
        if (this.cur.paused && !this.localPause) this.play()
      }
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
    } else {
      this.keepTime(np)
    }
    const listening = this.mode === 'listener'
    if (np.state === 'playing' || (!listening && np.state === 'loading')) {
      // Already playing it (we started the preloaded song when the last one
      // ended): no new 'playing' event will come, so tell the server now.
      if (np.state === 'loading' && !this.cur.paused && item.id === this.curItem?.id) this.report('playing')
      if (listening && this.localPause) this.set({ status: 'paused' })
      else this.play()
    } else if (listening && np.state === 'loading') {
      // Wait for the speaker to start it, unless we already have (gapless).
      if (this.cur.paused) this.set({ status: 'waiting' })
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

  /** Sets up for a room, unlocking the audio elements during the tap that started it. */
  private begin(roomId: string, mode: SpeakerMode, name: string, device: string) {
    const [a, b] = this.elements()
    for (const el of [a, b]) {
      this.offsets.set(el, 0)
      el.src = SILENCE
      el.play().catch(() => {})
    }
    clearInterval(this.resyncTimer)
    this.roomId = roomId
    this.mode = mode
    this.name = name
    this.device = device
    this.promoted = false
    this.localPause = false
    this.canSpeak = false
    this.rev = -1
    this.set({ status: 'waiting', mode, roomId })
    this.mediaHandlers()
    void this.keepAwake(speakerState.get().keepAwake)
  }

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
        if (document.visibilityState !== 'visible' || !this.active) return
        void this.keepAwake(speakerState.get().keepAwake)
        // Background tabs drift; a listener catches up on return.
        if (this.mode === 'listener') void this.resync()
      })
    }
    return this.els
  }

  /** Where el is in its song, in ms. */
  private position(el = this.cur) {
    return el.currentTime * 1000 + (this.offsets.get(el) ?? 0)
  }

  /** Points el at item's stream, from startMs into the song. */
  private setSrc(el: HTMLAudioElement, item: QueueItem, startMs = 0) {
    el.src = streamUrl(this.roomId!, item.id, acceptedTypes(), startMs)
    this.offsets.set(el, startMs)
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
      this.setSrc(this.cur, item)
    }
    this.curItem = item
    this.seekOnLoad(positionMs)
  }

  private seekOnLoad(positionMs: number) {
    const el = this.cur
    const item = this.curItem
    const seek = () => {
      if (el === this.cur && item === this.curItem && Math.abs(this.position(el) - positionMs) > 500) this.seekTo(positionMs)
    }
    if (el.readyState >= HTMLMediaElement.HAVE_METADATA) seek()
    else el.addEventListener('loadedmetadata', seek, { once: true })
  }

  /**
   * Moves the current song to positionMs. A stream that can't be seeked by
   * bytes (a transcoded one, as on iOS) is loaded again from there instead,
   * so joining part way through doesn't start the song over.
   */
  private seekTo(positionMs: number, reloadAfter = 0) {
    const el = this.cur
    const item = this.curItem
    if (!item) return
    const seconds = (positionMs - (this.offsets.get(el) ?? 0)) / 1000
    if (seconds >= 0 && canSeek(el.seekable, seconds)) {
      el.currentTime = seconds
      return
    }
    if (Math.abs(this.position(el) - positionMs) <= reloadAfter) return
    const playing = !el.paused
    this.expectPause()
    this.setSrc(el, item, positionMs)
    if (playing) this.play()
  }

  /** Corrects drift from the server's position for the song we're on. */
  private keepTime(np: Playback) {
    if (np.item?.id !== this.curItem?.id || this.cur.readyState < HTMLMediaElement.HAVE_METADATA) return
    const target = targetPosition(np)
    if (Math.abs(this.position() - target) > MAX_DRIFT) this.seekTo(target, MAX_DRIFT_RELOAD)
  }

  private preload(np: Playback) {
    const next = np.next
    if (!next || np.driver === 'remote' || next.id === this.preItem || next.id === this.curItem?.id) return
    this.pre.pause()
    this.setSrc(this.pre, next)
    this.pre.load()
    this.preItem = next.id
  }

  /** Plays the current element. onBlocked, if given, handles the browser refusing instead of asking for a tap. */
  private play(onBlocked?: () => void) {
    this.cur.play().then(
      () => this.set({ status: 'playing' }),
      (err: unknown) => {
        if (err instanceof DOMException && err.name === 'NotAllowedError') {
          if (onBlocked) onBlocked()
          else this.set({ status: 'blocked' })
        }
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
    // OS. A speaker tells the room; a listener only goes quiet itself.
    this.set({ status: 'paused' })
    if (this.mode === 'listener') this.localPause = true
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
    // server's round trip. Its new state confirms it. A listener does the
    // same, which keeps it in step with the speaker doing it too.
    const next = this.last?.next
    if (!next || this.preItem !== next.id) return
    if (this.inPlace && document.visibilityState === 'hidden') {
      this.playInPlace(next)
      return
    }
    this.load(next, 0)
    // iOS may refuse to start a different element while the screen is
    // locked; the one that just ended is still allowed to play.
    this.play(() => {
      this.inPlace = true
      this.playInPlace(next)
    })
  }

  /** Plays item in the element that just ended, rather than the preloaded one. */
  private playInPlace(item: QueueItem) {
    if (this.curItem?.id === item.id && this.pre.ended) {
      // load() already swapped to the preloaded element: swap back.
      this.expectPause()
      this.cur.pause()
      ;[this.cur, this.pre] = [this.pre, this.cur]
    }
    this.preItem = undefined
    this.curItem = item
    this.retries = 0
    this.setSrc(this.cur, item)
    this.play()
  }

  private onError() {
    if (!this.curItem || this.cur.src === SILENCE) return
    if (this.retries < RETRIES) {
      this.retries++
      setTimeout(() => this.recover(), 1000 * this.retries)
      return
    }
    if (this.mode === 'listener') {
      // Not ours to skip: wait quietly for the next song.
      this.set({ status: 'waiting' })
      return
    }
    const msg = this.cur.error?.message || `media error ${this.cur.error?.code ?? ''}`.trim()
    this.report('error', this.curItem, msg)
  }

  /** Reloads the current song where it left off, after a network blip. */
  private recover() {
    if (!this.curItem) return
    const at = this.position()
    this.setSrc(this.cur, this.curItem)
    this.seekOnLoad(at)
    const playing = this.last?.state === 'playing' || (this.mode === 'speaker' && this.last?.state === 'loading')
    if (playing && !this.localPause) this.play()
  }

  private halt() {
    clearInterval(this.resyncTimer)
    this.resyncTimer = undefined
    this.roomId = undefined
    this.curItem = undefined
    this.preItem = undefined
    this.last = undefined
    this.promoted = false
    this.localPause = false
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
    this.set({ status: 'off', mode: 'speaker', roomId: undefined })
  }

  private async reclaim() {
    if (this.reclaiming || !this.roomId) return
    this.reclaiming = true
    try {
      const np = await unwrap(
        api.PUT('/rooms/{roomId}/player', {
          params: { path: { roomId: this.roomId } },
          body: { deviceId: this.device, name: this.last?.player?.name ?? 'Speaker' },
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

  /**
   * A listener takes over as the speaker, because the speaker left. If
   * several listeners do at once, the last one wins and the others go back
   * to listening.
   */
  private async promote(resume: boolean) {
    const roomId = this.roomId
    if (this.reclaiming || !roomId) return
    this.reclaiming = true
    try {
      const np = await unwrap(
        api.PUT('/rooms/{roomId}/player', { params: { path: { roomId } }, body: { deviceId: this.device, name: this.name } }),
      )
      if (this.roomId !== roomId || this.mode !== 'listener') return
      this.mode = 'speaker'
      this.promoted = true
      this.set({ mode: 'speaker' })
      this.onState?.(np)
      this.apply(np)
      if (resume && np.state === 'paused') this.onState?.(await sendCommand(roomId, { action: 'play' }))
    } catch {
      // Not allowed, or someone beat us to it: keep listening.
    } finally {
      this.reclaiming = false
    }
  }

  private becomeListener() {
    this.mode = 'listener'
    this.promoted = false
    this.set({ mode: 'listener' })
    clearInterval(this.resyncTimer)
    this.resyncTimer = setInterval(() => void this.resync(), RESYNC_EVERY)
  }

  /** A listener asks where the room is, since the speaker's progress isn't pushed. */
  private async resync() {
    const roomId = this.roomId
    if (!roomId || this.mode !== 'listener') return
    try {
      const np = await unwrap(api.GET('/rooms/{roomId}/playback', { params: { path: { roomId } } }))
      this.onState?.(np)
      this.apply(np)
    } catch {
      // The socket will bring us the truth when the network is back.
    }
  }

  private report(event: 'playing' | 'progress' | 'paused' | 'ended' | 'error', item = this.curItem, error?: string) {
    const roomId = this.roomId
    if (!roomId || !item || this.mode !== 'speaker') return
    const positionMs = Math.round(item === this.curItem ? this.position() : 0)
    api
      .POST('/rooms/{roomId}/player/report', {
        params: { path: { roomId } },
        body: { deviceId: this.device, itemId: item.id, event, positionMs, error },
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
    // A listener's play and pause are its own: a friend listening from
    // across town shouldn't pause the party with their headphones.
    set('play', () => (this.mode === 'listener' ? this.resume() : command({ action: 'play' })))
    set('pause', () => {
      if (this.mode !== 'listener') return command({ action: 'pause' })
      this.localPause = true
      this.pause()
      this.set({ status: 'paused' })
    })
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
    const position = Math.min(this.cur ? this.position() / 1000 : 0, duration)
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
