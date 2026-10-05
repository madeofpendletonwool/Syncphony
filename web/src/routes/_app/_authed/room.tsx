import { createFileRoute, Link } from '@tanstack/react-router'
import { Plus, Sparkles } from 'lucide-react'
import { motion } from 'motion/react'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { fadeUp, stagger } from '@/lib/motion'

export const Route = createFileRoute('/_app/_authed/room')({
  component: Room,
})

// The live room view (now playing, up next, lanes) lands in MAD-695; this
// is the shell's empty state for it.
function Room() {
  return (
    <>
      <PageHeader title="Room" subtitle="Everyone takes turns. Add a song to your lane." />
      <motion.section
        variants={stagger}
        initial="hidden"
        animate="show"
        className="glass flex flex-col items-center gap-4 rounded-3xl px-6 py-14 text-center"
      >
        <motion.div variants={fadeUp} className="grid size-14 place-items-center rounded-2xl bg-primary/15 text-primary">
          <Sparkles className="size-7" />
        </motion.div>
        <motion.div variants={fadeUp}>
          <h2 className="text-headline">The queue is quiet</h2>
          <p className="mt-1 text-sm text-muted-foreground">Be the first to put something on.</p>
        </motion.div>
        <motion.div variants={fadeUp}>
          <Button asChild size="lg">
            <Link to="/search">
              <Plus data-icon="inline-start" />
              Find a song
            </Link>
          </Button>
        </motion.div>
      </motion.section>
    </>
  )
}
