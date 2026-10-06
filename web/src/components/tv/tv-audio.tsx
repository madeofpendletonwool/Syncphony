import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Speaker, Volume2 } from 'lucide-react'
import { useEffect, useRef } from 'react'
import { Equalizer } from '@/components/equalizer'
import { Button } from '@/components/ui/button'
import { newer, playbackQuery } from '@/lib/playback'
import { speaker, speakerState } from '@/lib/speaker'
import { useStore } from '@/lib/store'
import { cn } from '@/lib/utils'

const AUDIO_KEY = 'syncphony-tv-audio'

/**
 * The big screen as the room's speaker. Browsers only start audio after a
 * press, so it offers a button to press OK on with the remote. Once it's
 * playing, it starts again by itself after a reload, unless another device
 * has taken over in the meantime. If the browser still wants a press,
 * the button comes back.
 */
export function TvAudio({ roomId, device, name, onStopped }: {
  roomId: string
  /** Who the server knows this screen as: its display ID, or this browser's device ID. */
  device: string
  name: string
  /** It stopped being the speaker without being asked to (its audio was turned off, say). */
  onStopped?: () => void
}) {
  const queryClient = useQueryClient()
  const state = useStore(speakerState)
  const playback = useQuery({ ...playbackQuery(roomId), enabled: false })
  const here = state.roomId === roomId && state.status !== 'off'
  const other = playback.data?.player && playback.data.player.deviceId !== device ? playback.data.player : undefined

  // The server's state drives the speaker; its reports' replies flow back into the cache.
  useEffect(() => {
    speaker.onState = (np) => queryClient.setQueryData(playbackQuery(np.roomId).queryKey, (old) => newer(old, np))
    return () => {
      speaker.onState = undefined
      void speaker.stop()
    }
  }, [queryClient])
  useEffect(() => speaker.apply(playback.data), [playback.data])

  // Pick up where it left off after a reload, unless someone else is playing the room now.
  const resumed = useRef(false)
  useEffect(() => {
    if (resumed.current || !playback.data) return
    resumed.current = true
    const player = playback.data.player
    if (readFlag() && (!player || player.deviceId === device)) void speaker.start(roomId, name, device)
  }, [playback.data, roomId, name, device])

  // Stopped from elsewhere: let the page check whether it may still play.
  const was = useRef(here)
  useEffect(() => {
    if (was.current && !here) onStopped?.()
    was.current = here
  }, [here, onStopped])

  const start = () => {
    writeFlag(true)
    void speaker.start(roomId, name, device)
  }
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
