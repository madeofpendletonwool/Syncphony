import { useQueryClient } from '@tanstack/react-query'
import { AnimatePresence, motion } from 'motion/react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { errorMessage } from '@/api/errors'
import { SCENES, SHOWS, useScene } from '@/hooks/use-scene'
import { beatSettings, setBeatSettings } from '@/lib/beat'
import type { PlayerCommands } from '@/lib/now-playing'
import { newer, playbackQuery, sendCommand, type Playback, type PlaybackCommand } from '@/lib/playback'
import { cycle, tvKeyOf } from '@/lib/shortcuts'
import { useStore } from '@/lib/store'

const SHOWN_MS = 2200

/**
 * The big screen's keys (MAD-735), for a keyboard or a remote: space or K plays
 * and pauses, → skips (or votes to skip), ↑ and ↓ step through the visuals.
 * The visuals change on this screen only, starting from the room's pick;
 * auto is one of the steps. Each press says what it did, big, for a while.
 */
export function TvKeys({
  roomId,
  playback,
  commands: given,
  display,
  visualizing,
  show,
  showSetting,
  onShowSetting,
}: {
  roomId: string
  playback?: Playback
  /** What a signed-in screen may do to playback, as its user. */
  commands?: PlayerCommands
  /** A paired display that can play the room: it sends its own commands. */
  display?: boolean
  /** The visualizer has the screen, rather than the backdrop scene. */
  visualizing: boolean
  /** The show on screen, and the setting that picked it ('auto' or a show). */
  show: string
  showSetting: string
  onShowSetting: (setting: string) => void
}) {
  const scene = useScene()
  const { scene: sceneSetting } = useStore(beatSettings)
  // What the last press did. A look's name is read when shown: in auto,
  // the pick to name is the one the change makes.
  const [said, setSaid] = useState<{ id: number; text?: string }>()
  const counter = useRef(0)
  const say = useCallback((text?: string) => setSaid({ id: ++counter.current, text }), [])
  const displayCommands = useDisplayCommands(roomId, playback, !given && !!display, say)
  const commands = given ?? displayCommands

  const look = visualizing ? { setting: showSetting, picked: show } : { setting: sceneSetting, picked: scene }
  const lookName = look.setting === 'auto' ? `Auto · ${title(look.picked)}` : title(look.picked)

  useEffect(() => {
    if (!said) return
    const t = window.setTimeout(() => setSaid(undefined), SHOWN_MS)
    return () => window.clearTimeout(t)
  }, [said])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const key = tvKeyOf(e)
      if (!key) return
      e.preventDefault()
      const playing = playback?.state === 'playing' || playback?.state === 'loading'
      switch (key) {
        case 'playPause':
          if (!commands.toggle) {
            if (display && !playback?.player) return say('Press “Play the audio here” first')
            return say(playing ? 'Can’t pause from here' : 'Can’t play from here')
          }
          commands.toggle()
          return say(playing ? 'Paused' : 'Playing')
        case 'skip': {
          if (!playback?.item) return
          if (commands.next) {
            commands.next()
            return say('Skipped')
          }
          const vote = commands.vote
          if (!vote) return say('Can’t skip from here')
          vote.toggle()
          return say(vote.voted ? 'Took back your vote' : `Voted to skip · ${vote.count + 1} of ${vote.needed}`)
        }
        case 'nextLook':
        case 'previousLook': {
          const by = key === 'nextLook' ? 1 : -1
          if (visualizing) onShowSetting(cycle(['auto', ...SHOWS], showSetting, by))
          else setBeatSettings({ scene: cycle(['auto', ...SCENES], sceneSetting, by) })
          say()
        }
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [playback, commands, display, visualizing, showSetting, sceneSetting, onShowSetting, say])

  return (
    <div aria-live="polite" className="pointer-events-none fixed inset-x-0 top-[12vh] z-30 flex justify-center">
      <AnimatePresence mode="popLayout">
        {said && (
          <motion.p
            key={said.id}
            initial={{ opacity: 0, y: -12, scale: 0.96 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: -12, scale: 0.96 }}
            transition={{ duration: 0.3 }}
            className="glass-strong rounded-full px-[2vw] py-[1.2vh] text-[clamp(1rem,1.8vw,2rem)] font-semibold shadow-float"
          >
            {said.text ?? lookName}
          </motion.p>
        )}
      </AnimatePresence>
    </div>
  )
}

/**
 * A paired display's playback controls. It isn't a person, so it can't
 * vote; the server says whether it may play, pause and skip.
 */
function useDisplayCommands(
  roomId: string,
  playback: Playback | undefined,
  enabled: boolean,
  onError: (message: string) => void,
): PlayerCommands {
  const queryClient = useQueryClient()
  return useMemo(() => {
    if (!enabled || !playback) return {}
    const send = async (body: PlaybackCommand) => {
      try {
        const np = await sendCommand(roomId, body)
        queryClient.setQueryData(playbackQuery(roomId).queryKey, (old) => newer(old, np))
      } catch (err) {
        onError(errorMessage(err))
      }
    }
    const playing = playback.state === 'playing' || playback.state === 'loading'
    const item = playback.item
    return {
      // With no speaker, there's nothing to play on; the screen's own button starts one.
      toggle: playback.player ? () => void send({ action: playing ? 'pause' : 'play' }) : undefined,
      next: item ? () => void send({ action: 'skip', itemId: item.id }) : undefined,
    }
  }, [roomId, playback, enabled, queryClient, onError])
}

function title(s: string) {
  return s.charAt(0).toUpperCase() + s.slice(1)
}
