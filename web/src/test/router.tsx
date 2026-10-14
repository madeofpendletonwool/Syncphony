import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import type { ReactNode } from 'react'

/**
 * Renders ui as the only page of a router, for components with links in
 * them. The router mounts asynchronously: find what it shows with findBy*.
 */
export function InRouter({ children }: { children: ReactNode }) {
  const router = createRouter({
    routeTree: createRootRoute({ component: () => children }),
    history: createMemoryHistory(),
  })
  return <RouterProvider router={router} />
}
