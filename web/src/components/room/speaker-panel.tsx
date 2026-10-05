import { useQuery } from '@tanstack/react-query'
import { MonitorSmartphone, Speaker, Volume2 } from 'lucide-react'
import { motion } from 'motion/react'
import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { useMe } from '@/lib/auth'
import { canControl, playbackQuery } from '@/lib/playback'
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
  const allowed = canControl(room, me.id)

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
          <span className="relative grid size-9 shrink-0 place-items-center rounded-xl bg-primary text-primary-foreground">
            <Speaker className="size-5" />
            {state.status === 'playing' && (
              <motion.span
                aria-hidden
                className="absolute inset-0 rounded-xl ring-2 ring-primary"
                animate={{ scale: [1, 1.35], opacity: [0.7, 0] }}
                transition={{ duration: 1.4, repeat: Infinity, ease: 'easeOut' }}
              />
            )}
          </span>
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium">This phone is the speaker</p>
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
          <Toggle checked={state.keepAwake} onChange={(on) => speaker.setKeepAwake(on)} label="Keep the screen on" />
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
        {other ? 'Play on this phone instead' : 'Play on this phone'}
      </Button>
      <p className="text-caption text-muted-foreground">
        {other ? `Playing on ${other.name}` : 'Use the phone connected to the speaker'}
      </p>
    </div>
  )
}

function Toggle({ checked, onChange, label }: { checked: boolean; onChange: (on: boolean) => void; label: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      onClick={() => onChange(!checked)}
      className={cn(
        'relative h-7 w-12 shrink-0 rounded-full transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
        checked ? 'bg-primary' : 'bg-muted',
      )}
    >
      <motion.span
        layout
        transition={{ type: 'spring', stiffness: 600, damping: 35 }}
        className={cn('absolute top-1 size-5 rounded-full bg-white shadow', checked ? 'right-1' : 'left-1')}
      />
    </button>
  )
}
