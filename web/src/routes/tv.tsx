import { useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useCallback, useEffect, type ReactNode } from 'react'
import { TvPairing } from '@/components/tv/tv-pairing'
import { TvStage } from '@/components/tv/tv-stage'
import { meQuery } from '@/lib/auth'
import { displayMeQuery, type DisplayMe } from '@/lib/displays'
import { useRoomControls } from '@/hooks/use-room-controls'
import { box, isBox } from '@/lib/box'
import { can, playbackQuery } from '@/lib/playback'
import { useCurrentRoom } from '@/lib/room'
import { deviceId, speakerState } from '@/lib/speaker'
import { useStore } from '@/lib/store'

// Big-screen mode (MAD-716): a TV, projector or spare tablet showing one
// room. A signed-in device shows the room it's in, even if it was once
// paired too (its own room is the one you mean); a paired display shows
// its room; anything else shows a code to pair it. Either kind can
// also play the room's audio: a display if it was paired with audio on,
// a signed-in device if its user may be the speaker. On a box (ADR 0016),
// the page talks to the box's own daemon and plays by itself.
export const Route = createFileRoute('/tv')({
  component: Tv,
})

function Tv() {
  // Polled, so turning the screen's audio on or off from a phone shows up here.
  const display = useQuery({ ...displayMeQuery, refetchInterval: (q) => (q.state.data ? 30_000 : false) })
  const boxMode = isBox()
  // A box is always its pairing; anywhere else, whoever's signed in wins.
  const me = useQuery({ ...meQuery, enabled: !boxMode || (display.isSuccess && !display.data) })
  useEffect(() => {
    if (!boxMode) return
    box.start()
    return () => box.stop()
  }, [boxMode])
  useDarkTheme()

  if (display.isPending || (me.isPending && !(boxMode && display.data))) return <Blank />
  const paired = display.data ? <PairedStage me={display.data} /> : undefined
  if (paired && (boxMode || !me.data)) return paired
  if (me.data) return <SignedInStage orElse={paired} />
  return paired ?? <TvPairing />
}

function PairedStage({ me }: { me: DisplayMe }) {
  const queryClient = useQueryClient()
  const { display, room } = me
  // Tell the box which room's screen this is now.
  useEffect(() => {
    box.paired(room.id)
    return () => box.unpaired()
  }, [room.id])
  return (
    <TvStage
      roomId={room.id}
      roomName={room.name}
      screens={room.screens}
      paired
      onUnpaired={() => queryClient.setQueryData(displayMeQuery.queryKey, null)}
      audio={
        display.audio
          ? {
              device: display.id,
              name: display.name,
              // A box plays by itself; nobody's there to press OK.
              autoStart: isBox(),
              onStopped: () => void queryClient.invalidateQueries({ queryKey: displayMeQuery.queryKey }),
            }
          : undefined
      }
    />
  )
}

/**
 * A signed-in device shows the room it's in, without joining it. Out of
 * any room, it shows its pairing (orElse) if it has one.
 */
function SignedInStage({ orElse }: { orElse?: ReactNode }) {
  const { room, rooms } = useCurrentRoom()
  const me = useQuery(meQuery).data
  const speaking = useStore(speakerState)
  const navigate = useNavigate()
  const exit = useCallback(() => void navigate({ to: '/room' }), [navigate])
  const playback = useQuery({ ...playbackQuery(room?.id ?? ''), enabled: !!room })
  // The keys do what the buttons would: skip or vote, as the room allows.
  const commands = useRoomControls(room, playback.data, me ?? { id: '', displayName: '' }, !!me?.guest)
  if (rooms.isPending) return <Blank />
  if (!room) return orElse ?? <TvPairing />
  const maySpeak = !!me && !me.guest && can(room, me.id, 'speaker')
  const mayPlay = maySpeak && can(room, me.id, 'playPause')
  const maySkip = maySpeak && can(room, me.id, 'skip')
  // Already playing the room here (listening along, say): it carries on,
  // whether or not this user may be the speaker.
  const playingHere = speaking.roomId === room.id && speaking.status !== 'off'
  return (
    <TvStage
      roomId={room.id}
      roomName={room.name}
      screens={room.screens}
      paired={false}
      onUnpaired={() => {}}
      commands={me ? commands : {}}
      onExit={exit}
      audio={
        me && (maySpeak || playingHere)
          ? { device: deviceId(), name: `${me.displayName.split(' ')[0]}'s big screen`, canPlayPause: mayPlay, canSkip: maySkip }
          : undefined
      }
    />
  )
}

function Blank() {
  return <div className="h-dvh cursor-none bg-background" />
}

/** Big screens are always dark: kinder to a room with the lights down. */
function useDarkTheme() {
  useEffect(() => {
    const root = document.documentElement
    const was = root.classList.contains('dark')
    root.classList.add('dark')
    return () => {
      if (!was) root.classList.remove('dark')
    }
  }, [])
}
