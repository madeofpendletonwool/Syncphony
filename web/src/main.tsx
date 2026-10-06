import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, RouterProvider } from '@tanstack/react-router'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { ApiError } from './api/errors'
import './index.css'
import { meQuery, rememberMe } from './lib/auth'
import { registerServiceWorker } from './lib/pwa'
import { routeTree } from './routeTree.gen'

// A request that finds the session gone (expired, or signed out elsewhere)
// marks the user signed out; the signed-in layout then sends them to /login.
function onError(err: unknown) {
  if (err instanceof ApiError && err.code === 'unauthenticated') {
    rememberMe(null)
    queryClient.setQueryData(meQuery.queryKey, null)
  }
}

const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError }),
  mutationCache: new MutationCache({ onError }),
  defaultOptions: {
    queries: {
      // Don't retry what retrying can't fix.
      retry: (count, err) => !(err instanceof ApiError && err.status < 500) && count < 2,
    },
  },
})
const router = createRouter({ routeTree, context: { queryClient }, defaultPreload: 'intent' })

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

registerServiceWorker()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
)
