import { createFileRoute, redirect } from '@tanstack/react-router'

export const Route = createFileRoute('/_app/_authed/')({
  beforeLoad: () => {
    throw redirect({ to: '/room', replace: true })
  },
})
