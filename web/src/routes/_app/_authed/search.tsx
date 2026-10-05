import { createFileRoute } from '@tanstack/react-router'
import { Search as SearchIcon } from 'lucide-react'
import { useState } from 'react'
import { PageHeader } from '@/components/page-header'
import { Input } from '@/components/ui/input'

export const Route = createFileRoute('/_app/_authed/search')({
  component: Search,
})

// Results and add-to-lane arrive with MAD-694.
function Search() {
  const [q, setQ] = useState('')
  return (
    <>
      <PageHeader title="Search" />
      <div className="sticky top-[calc(env(safe-area-inset-top)+0.75rem)] z-10">
        <SearchIcon className="pointer-events-none absolute top-1/2 left-4 size-5 -translate-y-1/2 text-muted-foreground" />
        <Input
          type="search"
          inputMode="search"
          enterKeyHint="search"
          aria-label="Search songs, albums and artists"
          placeholder="Songs, albums, artists"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          className="glass h-13 rounded-2xl pl-12 text-base"
        />
      </div>
      <p className="mt-16 text-center text-sm text-muted-foreground">
        Search every service you&apos;ve linked, all at once.
      </p>
    </>
  )
}
