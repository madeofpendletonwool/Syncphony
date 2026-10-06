import type { QueryClient } from '@tanstack/react-query'
import { createRootRouteWithContext, Outlet, useRouter } from '@tanstack/react-router'
import { RefreshCw, WifiOff } from 'lucide-react'
import { MotionConfig } from 'motion/react'
import { Button } from '@/components/ui/button'

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  component: () => (
    <MotionConfig reducedMotion="user">
      <Outlet />
    </MotionConfig>
  ),
  errorComponent: Unreachable,
})

/** A screen couldn't load, almost always because the server can't be reached. */
function Unreachable() {
  const router = useRouter()
  const offline = !navigator.onLine
  return (
    <div className="grid min-h-dvh place-items-center px-gutter">
      <div className="glass flex max-w-sm flex-col items-center gap-3 rounded-3xl p-8 text-center">
        <span className="grid size-12 place-items-center rounded-2xl bg-muted text-muted-foreground">
          <WifiOff className="size-6" />
        </span>
        <h1 className="text-headline">{offline ? "You're offline" : "Can't reach Syncphony"}</h1>
        <p className="text-sm text-muted-foreground">
          {offline ? 'Connect to the internet and try again.' : 'The server may be restarting. Try again in a moment.'}
        </p>
        <Button onClick={() => void router.invalidate()} className="mt-2">
          <RefreshCw data-icon="inline-start" />
          Try again
        </Button>
      </div>
    </div>
  )
}
