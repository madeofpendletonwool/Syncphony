import { useQuery } from '@tanstack/react-query'
import { createFileRoute, Outlet, redirect, useRouter } from '@tanstack/react-router'
import { useEffect } from 'react'
import { RoomLive } from '@/components/room-live'
import { CrownMoment } from '@/components/room/crown-moment'
import { PageTransition } from '@/components/shell/app-shell'
import { meQuery } from '@/lib/auth'

export const Route = createFileRoute('/_app/_authed')({
  beforeLoad: async ({ context, location }) => {
    const me = await context.queryClient.ensureQueryData(meQuery)
    if (!me) throw redirect({ to: '/login', search: { redirect: location.href }, replace: true })
  },
  component: Authed,
})

function Authed() {
  const me = useQuery(meQuery).data
  const router = useRouter()
  // Signed out while here (session expired, or signed out elsewhere): run
  // beforeLoad again, which sends us to /login.
  useEffect(() => {
    if (me === null) void router.invalidate()
  }, [me, router])
  if (me === null) return null
  return (
    <>
      <RoomLive />
      <CrownMoment />
      <PageTransition>
        <Outlet />
      </PageTransition>
    </>
  )
}
