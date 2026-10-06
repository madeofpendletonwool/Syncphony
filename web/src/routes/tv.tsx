import { useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useEffect } from 'react'
import { TvPairing } from '@/components/tv/tv-pairing'
import { TvStage } from '@/components/tv/tv-stage'
import { meQuery } from '@/lib/auth'
import { displayMeQuery } from '@/lib/displays'
import { useCurrentRoom } from '@/lib/room'

// Big-screen mode (MAD-716): a TV, projector or spare tablet showing one
// room. A paired display shows its room; a signed-in device shows the
// room it's in; anything else shows a code to pair it.
export const Route = createFileRoute('/tv')({
  component: Tv,
})

function Tv() {
  const display = useQuery(displayMeQuery)
  const me = useQuery({ ...meQuery, enabled: display.isSuccess && !display.data })
  useDarkTheme()

  if (display.isPending || (display.isSuccess && !display.data && me.isPending)) return <Blank />
  if (display.data) return <PairedStage roomId={display.data.room.id} roomName={display.data.room.name} />
  if (me.data) return <SignedInStage />
  return <TvPairing />
}

function PairedStage({ roomId, roomName }: { roomId: string; roomName: string }) {
  const queryClient = useQueryClient()
  return (
    <TvStage
      roomId={roomId}
      roomName={roomName}
      paired
      onUnpaired={() => queryClient.setQueryData(displayMeQuery.queryKey, null)}
    />
  )
}

/** A signed-in device shows the room it's in, without joining it. */
function SignedInStage() {
  const { room, rooms } = useCurrentRoom()
  if (rooms.isPending) return <Blank />
  if (!room) return <TvPairing />
  return <TvStage roomId={room.id} roomName={room.name} paired={false} onUnpaired={() => {}} />
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
