import type { QueryClient } from '@tanstack/react-query'
import { createRootRouteWithContext, Outlet } from '@tanstack/react-router'
import { MotionConfig } from 'motion/react'

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  component: () => (
    <MotionConfig reducedMotion="user">
      <Outlet />
    </MotionConfig>
  ),
})
