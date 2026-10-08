import { useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useEffect } from 'react'
import { TvPairing } from '@/components/tv/tv-pairing'
import { TvStage } from '@/components/tv/tv-stage'
import { meQuery } from '@/lib/auth'
import { displayMeQuery, type DisplayMe } from '@/lib/displays'
import { can } from '@/lib/playback'
import { useCurrentRoom } from '@/lib/room'
import { deviceId } from '@/lib/speaker'

// Big-screen mode (MAD-716): a TV, projector or spare tablet showing one
// room. A paired display shows its room; a signed-in device shows the
// room it's in; anything else shows a code to pair it. Either kind can
// also play the room's audio: a display if it was paired with audio on,
// a signed-in device if its user may be the speaker.
export const Route = createFileRoute('/tv')({
  component: Tv,
})

function Tv() {
  // Polled, so turning the screen's audio on or off from a phone shows up here.
  const display = useQuery({ ...displayMeQuery, refetchInterval: (q) => (q.state.data ? 30_000 : false) })
  const me = useQuery({ ...meQuery, enabled: display.isSuccess && !display.data })
  useDarkTheme()

  if (display.isPending || (display.isSuccess && !display.data && me.isPending)) return <Blank />
  if (display.data) return <PairedStage me={display.data} />
  if (me.data) return <SignedInStage />
  return <TvPairing />
}

function PairedStage({ me }: { me: DisplayMe }) {
  const queryClient = useQueryClient()
  const { display, room } = me
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
              onStopped: () => void queryClient.invalidateQueries({ queryKey: displayMeQuery.queryKey }),
            }
          : undefined
      }
    />
  )
}

/** A signed-in device shows the room it's in, without joining it. */
function SignedInStage() {
  const { room, rooms } = useCurrentRoom()
  const me = useQuery(meQuery).data
  if (rooms.isPending) return <Blank />
  if (!room) return <TvPairing />
  const maySpeak = !!me && !me.guest && can(room, me.id, 'speaker')
  const mayPlay = maySpeak && can(room, me.id, 'playPause')
  const maySkip = maySpeak && can(room, me.id, 'skip')
  return (
    <TvStage
      roomId={room.id}
      roomName={room.name}
      screens={room.screens}
      paired={false}
      onUnpaired={() => {}}
      audio={
        maySpeak
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
