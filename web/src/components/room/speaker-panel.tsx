import { useQuery } from '@tanstack/react-query'
import { Headphones, MonitorSmartphone, Speaker, Volume2 } from 'lucide-react'
import { useState } from 'react'
import { Equalizer } from '@/components/equalizer'
import { Switch } from '@/components/ui/switch'
import { Button } from '@/components/ui/button'
import { useMe } from '@/lib/auth'
import { can, playbackQuery, sendCommand } from '@/lib/playback'
import type { Room } from '@/lib/room'
import { deviceId, speaker, speakerName, speakerState } from '@/lib/speaker'
import { useStore } from '@/lib/store'
import { cn } from '@/lib/utils'
import { deviceName } from '@/lib/webauthn'

/**
 * Who's playing the room's audio, and the switches to make this phone the
 * speaker (the one connected to the Bluetooth speaker), or to listen along
 * on it from anywhere.
 */
export function SpeakerPanel({ room, prominent }: { room: Room; prominent?: boolean }) {
  const me = useMe()
  const state = useStore(speakerState)
  const playback = useQuery({ ...playbackQuery(room.id), enabled: false })
  const [starting, setStarting] = useState(false)
  const here = state.roomId === room.id && state.status !== 'off'
  const player = playback.data?.player
  const other = player && player.deviceId !== deviceId() ? player : undefined
  const allowed = !me.guest && can(room, me.id, 'speaker')
  // Guests are in the room with the speaker; listening along is for members.
  const mayListen = !me.guest
  const thisDevice = deviceNoun()
  const name = speakerName(me.displayName)

  const start = () => {
    setStarting(true)
    const takingOver = !!other
    void speaker
      .start(room.id, name)
      .then(async (np) => {
        // "Play on this phone" means play: start a paused room too. Taking
        // over from another speaker leaves it as it was.
        if (!np || takingOver || np.state !== 'paused' || !can(room, me.id, 'playPause')) return
        speaker.onState?.(await sendCommand(room.id, { action: 'play' }))
      })
      .catch(() => {
        // The speaker is set up either way; play is a tap away.
      })
      .finally(() => setStarting(false))
  }
  const listen = () => speaker.listen(room.id, name, playback.data, allowed)

  if (here) {
    const listening = state.mode === 'listener'
    return (
      <div className="flex flex-col gap-3">
        {state.status === 'blocked' && (
          <Button size="lg" onClick={() => speaker.resume()} className="w-full">
            <Volume2 data-icon="inline-start" />
            Tap to start the audio
          </Button>
        )}
        <div className="flex items-center gap-3 rounded-2xl bg-primary/12 p-3">
          <span className="grid size-9 shrink-0 place-items-center rounded-xl bg-primary text-primary-foreground">
            {state.status === 'playing' || state.status === 'paused' ? (
              <Equalizer playing={state.status === 'playing'} />
            ) : listening ? (
              <Headphones className="size-5" />
            ) : (
              <Speaker className="size-5" />
            )}
          </span>
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium">{listening ? `Listening on this ${thisDevice}` : `This ${thisDevice} is the speaker`}</p>
            <p className="text-caption text-muted-foreground">
              {listening ? listenerHint(state.status, other?.name, playback.data?.state) : speakerHint(state.status)}
            </p>
          </div>
          {listening && state.status === 'paused' && playback.data?.state === 'playing' && (
            <Button size="sm" variant="ghost" onClick={() => speaker.resume()}>
              Resume
            </Button>
          )}
          <Button size="sm" variant="ghost" onClick={() => void speaker.stop()}>
            Stop
          </Button>
        </div>
        {listening && allowed && other && (
          <Button size="sm" variant="ghost" onClick={start} disabled={starting} className="self-center text-muted-foreground">
            <Speaker data-icon="inline-start" />
            Make this the speaker instead
          </Button>
        )}
        <label className="flex cursor-pointer items-center justify-between gap-3 px-1 text-sm">
          <span className="flex items-center gap-2 text-muted-foreground">
            <MonitorSmartphone className="size-4" />
            Keep the screen on
          </span>
          <Switch checked={state.keepAwake} onChange={(on) => speaker.setKeepAwake(on)} label="Keep the screen on" />
        </label>
      </div>
    )
  }

  const listenButton = (
    <Button size={prominent ? 'lg' : 'default'} variant={prominent ? 'default' : 'glass'} onClick={listen} className={cn(prominent && 'w-full')}>
      <Headphones data-icon="inline-start" />
      Listen along here
    </Button>
  )

  if (!allowed) {
    return (
      <div className="flex flex-col items-center gap-2">
        {mayListen && other && listenButton}
        <p className="flex items-center justify-center gap-1.5 text-caption text-muted-foreground">
          <Speaker className="size-3.5" />
          {other
            ? `Playing on ${other.name}`
            : me.guest
              ? 'Waiting for the host to start the speaker'
              : 'Only the room owner can start the speaker'}
        </p>
      </div>
    )
  }

  return (
    <div className="flex flex-col items-center gap-2">
      <div className={cn('flex flex-wrap items-center justify-center gap-2', prominent && 'w-full flex-col')}>
        {other && mayListen && listenButton}
        <Button
          size={prominent ? 'lg' : 'default'}
          variant={prominent && !other ? 'default' : 'glass'}
          onClick={start}
          disabled={starting}
          className={cn(prominent && 'w-full')}
        >
          <Speaker data-icon="inline-start" />
          {other ? 'Be the speaker' : `Play on this ${thisDevice}`}
        </Button>
      </div>
      <p className="text-center text-caption text-muted-foreground">
        {other ? `Playing on ${other.name}. Listen along from anywhere, or take over here.` : 'Use the device connected to the speaker'}
      </p>
    </div>
  )
}

function speakerHint(status: string) {
  if (status === 'remote') return 'This song plays on its own service'
  if (status === 'waiting') return 'Waiting for a song'
  if (status === 'blocked') return 'The browser needs a tap first'
  return 'Leave this open; it keeps playing locked'
}

function listenerHint(status: string, speakerWho?: string, roomState?: string) {
  if (status === 'remote') return 'This song plays on its own service'
  if (status === 'blocked') return 'The browser needs a tap first'
  if (status === 'paused') return roomState === 'playing' ? 'Paused here; the room plays on' : 'The room is paused'
  if (!speakerWho) return 'Waiting for a speaker'
  if (status === 'waiting') return `Waiting for ${speakerWho}`
  return `In time with ${speakerWho}`
}

/** What to call this device in "play on this …". */
function deviceNoun() {
  const name = deviceName()
  if (name === 'iPhone' || name === 'Android') return 'phone'
  if (name === 'iPad') return 'tablet'
  return 'device'
}
