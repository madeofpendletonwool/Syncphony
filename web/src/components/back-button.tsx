import { useCanGoBack, useRouter } from '@tanstack/react-router'
import { ChevronLeft } from 'lucide-react'
import { Button } from '@/components/ui/button'

/** Back to wherever you came from (usually search results), or to Search. */
export function BackButton({ fallback = '/search' }: { fallback?: '/search' | '/library' }) {
  const router = useRouter()
  const canGoBack = useCanGoBack()
  return (
    <div className="pt-6 pb-2">
      <Button
        variant="glass"
        size="icon"
        aria-label="Back"
        onClick={() => (canGoBack ? router.history.back() : void router.navigate({ to: fallback }))}
      >
        <ChevronLeft className="size-5" />
      </Button>
    </div>
  )
}
