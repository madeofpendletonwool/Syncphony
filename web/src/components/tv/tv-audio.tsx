import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Pause, Play, SkipForward, Speaker, Volume2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { errorMessage } from '@/api/errors'
import { Equalizer } from '@/components/equalizer'
import { Button } from '@/components/ui/button'
import { newer, playbackQuery, sendCommand, type PlaybackCommand } from '@/lib/playback'
import { speaker, speakerState } from '@/lib/speaker'
import { useStore } from '@/lib/store'
import { cn } from '@/lib/utils'

const AUDIO_KEY = 'syncphony-tv-audio'

/**
 * The big screen as the room's speaker. Browsers only start audio after a
 * press, so it offers a button to press OK on with the remote. Once it's
 * playing, it starts again by itself after a reload, unless another device
 * has taken over in the meantime. If the browser still wants a press,
 * the button comes back. Opening the big screen on a device that's
 * already the speaker carries on playing. It has a speaker's buttons
 * too: play, pause and skip. A box (ADR 0016) starts without the press:
 * its Chromium allows autoplay, and if play() still rejects, the button
 * is the fallback.
 */
export function TvAudio({ roomId, device, name, onStopped, canPlayPause = true, canSkip = true, autoStart = false }: {
  roomId: string
  /** Who the server knows this screen as: its display ID, or this browser's device ID. */
  device: string
  name: string
  /** It stopped being the speaker without being asked to (its audio was turned off, say). */
  onStopped?: () => void
  /** Whether to offer play and pause, and skip. The server has the last word. */
  canPlayPause?: boolean
  canSkip?: boolean
  /** A box plays by itself the first time, not just after a reload. */
  autoStart?: boolean
}) {
  const queryClient = useQueryClient()
  const state = useStore(speakerState)
  const playback = useQuery({ ...playbackQuery(roomId), enabled: false })
  const here = state.roomId === roomId && state.status !== 'off'
  const other = playback.data?.player && playback.data.player.deviceId !== device ? playback.data.player : undefined

  // The server's state drives the speaker; its reports' replies flow back
  // into the cache. Already playing this room (we came from the app): keep
  // going, and pick it up again after a reload. Leaving hands it back.
  useEffect(() => {
    speaker.onState = (np) => queryClient.setQueryData(playbackQuery(np.roomId).queryKey, (old) => newer(old, np))
    if (speaker.keep(roomId)) writeFlag(true)
    return () => {
      speaker.onState = undefined
      speaker.stopSoon()
    }
  }, [queryClient, roomId])
  useEffect(() => speaker.apply(playback.data), [playback.data])

  // Pick up where it left off after a reload, unless someone else is
  // playing the room now. A box skips the flag: its Chromium needs no
  // press, so it also starts the first time.
  const resumed = useRef(false)
  useEffect(() => {
    if (resumed.current || !playback.data) return
    resumed.current = true
    if (speaker.active) return
    const player = playback.data.player
    if ((readFlag() || autoStart) && (!player || player.deviceId === device)) void speaker.start(roomId, name, device)
  }, [playback.data, roomId, name, device, autoStart])

  // Stopped from elsewhere: let the page check whether it may still play.
  const was = useRef(here)
  useEffect(() => {
    if (was.current && !here) onStopped?.()
    was.current = here
  }, [here, onStopped])

  const [error, setError] = useState<string>()
  useEffect(() => {
    if (!error) return
    const t = window.setTimeout(() => setError(undefined), 5000)
    return () => window.clearTimeout(t)
  }, [error])
  const command = async (body: PlaybackCommand) => {
    try {
      const np = await sendCommand(roomId, body)
      queryClient.setQueryData(playbackQuery(roomId).queryKey, (old) => newer(old, np))
    } catch (err) {
      setError(errorMessage(err))
    }
  }
  // Playing here means playing: a paused room starts too, unless we're
  // taking over from another speaker, which carries on as it was.
  const start = () => {
    writeFlag(true)
    const takingOver = !!other
    void speaker.start(roomId, name, device).then((np) => {
      if (np && !takingOver && np.state === 'paused' && canPlayPause) void command({ action: 'play' })
    })
  }
  const np = playback.data
  const playing = np?.state === 'playing' || np?.state === 'loading'
  // Play with no speaker makes this screen it, in the one press.
  const toggle = () => (playing ? command({ action: 'pause' }) : np?.player ? command({ action: 'play' }) : start())
  const transport = canPlayPause && np && (np.item || np.next) && (
    <div className="flex items-center gap-[0.5vw]">
      <Button size="icon-lg" variant="glass" aria-label={playing ? 'Pause' : 'Play'} onClick={() => void toggle()} className={tvIcon}>
        {playing ? <Pause className="fill-current" /> : <Play className="fill-current" />}
      </Button>
      {canSkip && np.item && (
        <Button
          size="icon-lg"
          variant="ghost"
          aria-label="Next"
          onClick={() => void command({ action: 'skip', itemId: np.item?.id })}
          className={tvIcon}
        >
          <SkipForward className="fill-current" />
        </Button>
      )}
    </div>
  )
  const problem = error && <span className="text-[clamp(0.85rem,1.1vw,1.15rem)] text-destructive">{error}</span>
  const stop = () => {
    writeFlag(false)
    void speaker.stop()
  }

  if (here && state.status === 'blocked') {
    return (
      <Button autoFocus size="lg" onClick={() => speaker.resume()} className={tvButton}>
        <Volume2 data-icon="inline-start" />
        Press OK to start the audio
      </Button>
    )
  }

  // Keyed apart, so focus on the play button doesn't carry over to Stop:
  // a second press of OK shouldn't silence the room.
  if (here) {
    return (
      <div key="here" className="flex items-center gap-[1vw]">
        {problem}
        {transport}
        <span className="flex items-center gap-[0.6vw] text-[clamp(0.9rem,1.3vw,1.35rem)] text-muted-foreground">
          {state.status === 'playing' ? <Equalizer playing className="text-primary" /> : <Speaker className="size-[2.4vh]" />}
          {state.status === 'remote'
            ? 'This song plays on its own service'
            : state.status === 'waiting'
              ? 'Ready to play here'
              : state.status === 'paused'
                ? 'Paused here'
                : 'Playing here'}
        </span>
        <Button variant="ghost" onClick={stop} className="text-[clamp(0.85rem,1.1vw,1.15rem)] text-muted-foreground focus-visible:ring-4">
          Stop
        </Button>
      </div>
    )
  }

  return (
    <div key="offer" className="flex items-center gap-[1vw]">
      {problem}
      {other && transport}
      {other && <span className="text-[clamp(0.9rem,1.3vw,1.35rem)] text-muted-foreground">Playing on {other.name}</span>}
      <Button autoFocus variant={other ? 'glass' : 'default'} size="lg" onClick={start} className={tvButton}>
        <Speaker data-icon="inline-start" />
        {other ? 'Play here instead' : 'Play the audio here'}
      </Button>
    </div>
  )
}

// Big enough to read across a room, with a focus ring a remote's D-pad can find.
const tvButton = cn('h-auto rounded-[1.6vh] px-[1.6vw] py-[1.2vh] text-[clamp(1rem,1.4vw,1.5rem)] focus-visible:ring-4')
const tvIcon = cn('size-[clamp(2.5rem,4vw,4.5rem)] rounded-full focus-visible:ring-4 [&_svg]:size-[45%]')

function readFlag() {
  try {
    return localStorage.getItem(AUDIO_KEY) === '1'
  } catch {
    return false
  }
}

function writeFlag(on: boolean) {
  try {
    localStorage.setItem(AUDIO_KEY, on ? '1' : '0')
  } catch {
    // Private mode: it asks for a press again after a reload.
  }
}
