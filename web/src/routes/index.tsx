import { useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { motion } from 'motion/react'
import { api } from '@/api/client'
import { cn } from '@/lib/utils'

export const Route = createFileRoute('/')({
  component: Home,
})

function Home() {
  const health = useQuery({
    queryKey: ['health'],
    queryFn: async () => {
      const { data, error } = await api.GET('/healthz')
      if (error || !data) throw new Error('server unreachable')
      return data
    },
    refetchInterval: 10_000,
  })

  return (
    <main className="relative grid min-h-dvh place-items-center overflow-hidden px-4">
      <div
        aria-hidden
        className="pointer-events-none absolute -top-1/3 left-1/2 size-[48rem] -translate-x-1/2 rounded-full bg-[radial-gradient(closest-side,oklch(0.55_0.22_300/0.35),transparent)] blur-3xl"
      />
      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.5, ease: 'easeOut' }}
        className="relative flex flex-col items-center gap-4 text-center"
      >
        <h1 className="bg-gradient-to-br from-violet-300 to-pink-400 bg-clip-text text-5xl font-semibold tracking-tight text-transparent">
          Syncphony
        </h1>
        <p className="max-w-sm text-muted-foreground">
          One queue for everyone&apos;s music. Fair turns, every service.
        </p>
        <span
          className={cn(
            'inline-flex items-center gap-2 rounded-full border px-3 py-1 text-xs',
            health.isSuccess ? 'border-emerald-500/30 text-emerald-300' : 'border-border text-muted-foreground',
          )}
        >
          <span
            className={cn(
              'size-1.5 rounded-full',
              health.isSuccess ? 'bg-emerald-400' : health.isError ? 'bg-red-400' : 'bg-muted-foreground',
            )}
          />
          {health.isSuccess
            ? `server ${health.data.version}`
            : health.isError
              ? 'server unreachable'
              : 'connecting…'}
        </span>
      </motion.div>
    </main>
  )
}
