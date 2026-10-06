import { useQuery } from '@tanstack/react-query'
import { MonitorSmartphone, Speaker, Volume2 } from 'lucide-react'
import { useState } from 'react'
import { Equalizer } from '@/components/equalizer'
import { Switch } from '@/components/ui/switch'
import { Button } from '@/components/ui/button'
import { useMe } from '@/lib/auth'
import { can, playbackQuery } from '@/lib/playback'
import type { Room } from '@/lib/room'
import { speaker, speakerState } from '@/lib/speaker'
import { useStore } from '@/lib/store'
import { cn } from '@/lib/utils'
import { deviceName } from '@/lib/webauthn'

/**
 * Who's playing the room's audio, and the switch to make this phone the
 * speaker (the one connected to the Bluetooth speaker).
 */
export function SpeakerPanel({ room, prominent }: { room: Room; prominent?: boolean }) {
  const me = useMe()
  const state = useStore(speakerState)
  const playback = useQuery({ ...playbackQuery(room.id), enabled: false })
  const [starting, setStarting] = useState(false)
  const here = state.roomId === room.id && state.status !== 'off'
  const other = playback.data?.player
  const allowed = can(room, me.id, 'speaker')
  const thisDevice = deviceNoun()

  const start = () => {
    setStarting(true)
    void speaker.start(room.id, `${me.displayName.split(' ')[0]}'s ${deviceName()}`).finally(() => setStarting(false))
  }

  if (here) {
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
            ) : (
              <Speaker className="size-5" />
            )}
          </span>
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium">This {thisDevice} is the speaker</p>
            <p className="text-caption text-muted-foreground">
              {state.status === 'remote'
                ? 'This song plays on its own service'
                : state.status === 'waiting'
                  ? 'Waiting for a song'
                  : state.status === 'blocked'
                    ? 'The browser needs a tap first'
                    : 'Leave this open; it keeps playing locked'}
            </p>
          </div>
          <Button size="sm" variant="ghost" onClick={() => void speaker.stop()}>
            Stop
          </Button>
        </div>
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

  if (!allowed) {
    return (
      <p className="flex items-center justify-center gap-1.5 text-caption text-muted-foreground">
        <Speaker className="size-3.5" />
        {other ? `Playing on ${other.name}` : 'Only the room owner can start the speaker'}
      </p>
    )
  }

  return (
    <div className="flex flex-col items-center gap-2">
      <Button
        size={prominent ? 'lg' : 'default'}
        variant={prominent ? 'default' : 'glass'}
        onClick={start}
        disabled={starting}
        className={cn(prominent && 'w-full')}
      >
        <Speaker data-icon="inline-start" />
        {other ? `Play on this ${thisDevice} instead` : `Play on this ${thisDevice}`}
      </Button>
      <p className="text-caption text-muted-foreground">
        {other ? `Playing on ${other.name}` : 'Use the device connected to the speaker'}
      </p>
    </div>
  )
}

/** What to call this device in "play on this …". */
function deviceNoun() {
  const name = deviceName()
  if (name === 'iPhone' || name === 'Android') return 'phone'
  if (name === 'iPad') return 'tablet'
  return 'device'
}
