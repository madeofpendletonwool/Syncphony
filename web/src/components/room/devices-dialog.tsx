import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Laptop, MonitorPlay, Smartphone, Speaker } from 'lucide-react'
import type { ReactNode } from 'react'
import { errorMessage } from '@/api/errors'
import { Equalizer } from '@/components/equalizer'
import { Sheet } from '@/components/sheet'
import { Button } from '@/components/ui/button'
import { useMe } from '@/lib/auth'
import { deviceDetail, devicesQuery, makeSpeaker, stopSpeaker, type RoomDevice } from '@/lib/devices'
import { can, newer, playbackQuery } from '@/lib/playback'
import type { Room } from '@/lib/room'
import { deviceId, speaker, speakerName } from '@/lib/speaker'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'

/**
 * Everything connected to the room: its paired screens, and everyone's
 * browsers with it open. From here, the speaker moves to a screen (a box
 * starts by itself) or to another of your own computers, or stops,
 * without walking over to it. Phones become the speaker from themselves:
 * their browsers only play after a tap there. This one can, from here.
 */
export function DevicesDialog({ room, open, onOpenChange }: { room: Room; open: boolean; onOpenChange: (open: boolean) => void }) {
  const me = useMe()
  const queryClient = useQueryClient()
  // Comings and goings arrive on the room socket (devices.updated).
  const devices = useQuery({ ...devicesQuery(room.id), enabled: open })
  const playback = useQuery({ ...playbackQuery(room.id), enabled: false })
  const player = playback.data?.player
  const playing = playback.data?.state === 'playing' || playback.data?.state === 'loading'
  const host = me.role === 'admin' || room.ownerId === me.id
  const maySpeakHere = !me.guest && can(room, me.id, 'speaker')

  const speak = useMutation({
    mutationFn: (d: RoomDevice) => makeSpeaker(room.id, d.id),
    onSuccess: (_, d) => toast({ message: `Handing the speaker to ${d.name}` }),
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const stop = useMutation({
    mutationFn: (id: string) => stopSpeaker(room.id, id),
    onSuccess: (np) => queryClient.setQueryData(playbackQuery(room.id).queryKey, (old) => newer(old, np)),
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  // This browser: play here from the tap, which every browser allows.
  const here = () => void speaker.start(room.id, speakerName(me.displayName))

  const list = devices.data ?? []
  // The speaker may be a browser that's gone quiet (a locked phone): still
  // show it, so it can be stopped.
  const missing = player && !list.some((d) => d.id === player.deviceId) ? player : undefined

  return (
    <Sheet
      open={open}
      onOpenChange={onOpenChange}
      title="Devices"
      description="Everything connected to the room. Send the music to a screen or one of your computers from here."
    >
      <ul className="flex flex-col gap-1">
        {missing && (
          <Row
            icon={<Speaker className="size-4" />}
            name={missing.name}
            detail="The speaker · not connected"
            speaker
            playing={playing}
            action={
              (host || missing.userId === me.id) && (
                <Button size="sm" variant="ghost" disabled={stop.isPending} onClick={() => stop.mutate(missing.deviceId)}>
                  Stop
                </Button>
              )
            }
          />
        )}
        {list.map((d) => {
          const isSpeaker = player?.deviceId === d.id
          const mine = d.userId === me.id && d.id === deviceId()
          let action = null
          if (isSpeaker && d.canStop) {
            action = (
              <Button size="sm" variant="ghost" disabled={stop.isPending} onClick={() => stop.mutate(d.id)}>
                Stop
              </Button>
            )
          } else if (!isSpeaker && mine && maySpeakHere) {
            action = (
              <Button size="sm" variant="glass" onClick={here}>
                Play here
              </Button>
            )
          } else if (!isSpeaker && d.canSpeak) {
            action = (
              <Button size="sm" variant="glass" disabled={speak.isPending} onClick={() => speak.mutate(d)}>
                Make speaker
              </Button>
            )
          }
          return (
            <Row
              key={`${d.userId ?? ''}/${d.id}`}
              icon={kindIcon(d.kind)}
              name={d.name}
              detail={deviceDetail(d, mine, isSpeaker)}
              speaker={isSpeaker}
              playing={playing}
              dim={!d.online}
              action={action}
            />
          )
        })}
      </ul>
      {devices.isSuccess && list.length === 0 && !missing && (
        <p className="text-center text-sm text-muted-foreground">Nothing's connected right now.</p>
      )}
      <p className="text-caption text-muted-foreground">
        A phone becomes the speaker from the phone itself: its browser only plays after a tap there.
      </p>
    </Sheet>
  )
}

function Row({
  icon,
  name,
  detail,
  speaker,
  playing,
  dim,
  action,
}: {
  icon: ReactNode
  name: string
  detail: string
  speaker?: boolean
  playing: boolean
  dim?: boolean
  action?: ReactNode
}) {
  return (
    <li className={cn('flex items-center gap-3 rounded-2xl px-3.5 py-2.5', speaker ? 'bg-primary/12' : 'bg-muted/60')}>
      <span
        className={cn(
          'grid size-8 shrink-0 place-items-center rounded-xl',
          speaker ? 'bg-primary text-primary-foreground' : 'text-muted-foreground',
          dim && 'opacity-50',
        )}
      >
        {speaker ? <Equalizer playing={playing} /> : icon}
      </span>
      <div className={cn('min-w-0 flex-1', dim && 'opacity-60')}>
        <p className="truncate text-sm font-medium">{name}</p>
        <p className="truncate text-caption text-muted-foreground">{detail}</p>
      </div>
      {action}
    </li>
  )
}

function kindIcon(kind: RoomDevice['kind']) {
  if (kind === 'screen') return <MonitorPlay className="size-4" />
  if (kind === 'computer') return <Laptop className="size-4" />
  return <Smartphone className="size-4" />
}
