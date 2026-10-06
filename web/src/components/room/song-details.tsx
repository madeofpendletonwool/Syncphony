import { BookOpen, Mic2 } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useState } from 'react'
import { LinerNotesPanel } from '@/components/liner-notes-panel'
import { LyricsView } from '@/components/lyrics/lyrics-view'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { easeOutExpo, fadeUp } from '@/lib/motion'
import type { NowPlaying, PlayerCommands } from '@/lib/now-playing'

type Tab = 'lyrics' | 'notes'

const KEY = 'syncphony-song-tab'

function readTab(): Tab {
  try {
    return localStorage.getItem(KEY) === 'notes' ? 'notes' : 'lyrics'
  } catch {
    return 'lyrics'
  }
}

/** The playing song's lyrics and liner notes, under the room's now playing. */
export function SongDetails({ np, commands }: { np: NowPlaying; commands: PlayerCommands }) {
  const [tab, setTab] = useState<Tab>(readTab)
  const choose = (t: string) => {
    if (t !== 'lyrics' && t !== 'notes') return
    setTab(t)
    try {
      localStorage.setItem(KEY, t)
    } catch {
      // Private mode: remembered until reload.
    }
  }

  return (
    <motion.section variants={fadeUp} className="glass flex flex-col gap-4 rounded-3xl p-5">
      <ToggleGroup type="single" value={tab} onValueChange={choose} aria-label="Song details" className="self-start">
        <ToggleGroupItem value="lyrics">
          <Mic2 />
          Lyrics
        </ToggleGroupItem>
        <ToggleGroupItem value="notes">
          <BookOpen />
          Liner notes
        </ToggleGroupItem>
      </ToggleGroup>
      <AnimatePresence mode="wait" initial={false}>
        <motion.div
          key={`${tab}-${np.itemId}`}
          initial={{ opacity: 0, y: 8 }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0, y: -8 }}
          transition={{ duration: 0.3, ease: easeOutExpo }}
          className="flex flex-col"
        >
          {tab === 'lyrics' ? (
            <LyricsView np={np} onSeek={commands.seek} className="h-[min(26rem,55dvh)]" />
          ) : (
            <LinerNotesPanel np={np} />
          )}
        </motion.div>
      </AnimatePresence>
    </motion.section>
  )
}
