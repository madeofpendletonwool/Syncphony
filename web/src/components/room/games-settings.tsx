import { ChevronDown } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useState, type ReactNode } from 'react'
import { Switch } from '@/components/ui/switch'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { GAMES, LEVELS, plays, setGamesMuted, useGamesMuted, type GameKind, type GamesChange, type RoomGames } from '@/lib/games'
import { easeOutExpo } from '@/lib/motion'
import type { Room } from '@/lib/room'
import { cn } from '@/lib/utils'

// Rounds every so many songs; 0 is only when someone starts one.
const FREQUENCIES = [0, 1, 2, 3, 5]
const BREAKS = [0, 2, 4, 6]

const SCORES: { id: RoomGames['scores']; label: string; hint: string }[] = [
  { id: 'off', label: 'Off', hint: 'Just for fun: nobody keeps score' },
  { id: 'private', label: 'Your own', hint: 'Everyone sees their own score, and nobody else’s' },
  { id: 'board', label: 'Leaderboard', hint: 'A leaderboard on the big screen after each round' },
]

/**
 * The room's games (MAD-784): one level, in plain words, with the fine
 * controls under Adjust. A level change applies from the next song.
 */
export function GamesSettings({
  room,
  onChange,
  onStartRounds,
  ownerLabel,
}: {
  room: Room
  /** Replaces the room's games; what's left out takes the level's default. */
  onChange: (games: GamesChange) => void
  onStartRounds: (level: Room['permissions']['startRounds']) => void
  ownerLabel: string
}) {
  const g = room.games
  const [adjust, setAdjust] = useState(false)
  const level = LEVELS.find((l) => l.id === g.level) ?? LEVELS[0]
  // Fine-tuning sends everything as it is, with the one change.
  const tune = (change: Partial<RoomGames>) => onChange({ ...g, ...change })
  const muted = useGamesMuted()

  return (
    <>
      <div className="flex flex-col gap-2">
        <ToggleGroup
          type="single"
          value={g.level}
          // A new level brings its own defaults; who plays and where stay.
          onValueChange={(v) => v && v !== g.level && onChange({ level: v as RoomGames['level'], guests: g.guests, tvOnly: g.tvOnly })}
          aria-label="Games"
          className="flex-wrap"
        >
          {LEVELS.map((l) => (
            <ToggleGroupItem key={l.id} value={l.id}>
              {l.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <p className="text-caption text-muted-foreground">{level.hint}</p>
      </div>

      {plays(g.level) && (
        <div className="-mt-2 flex flex-col gap-5">
          <button
            type="button"
            aria-expanded={adjust}
            onClick={() => setAdjust((a) => !a)}
            className="-mx-1 flex items-center gap-1.5 self-start rounded-lg px-1 text-sm font-medium text-muted-foreground transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            Adjust
            <ChevronDown className={cn('size-4 transition-transform', adjust && 'rotate-180')} />
          </button>
          <AnimatePresence initial={false}>
            {adjust && (
              <motion.div
                initial={{ opacity: 0, height: 0 }}
                animate={{ opacity: 1, height: 'auto' }}
                exit={{ opacity: 0, height: 0 }}
                transition={{ duration: 0.25, ease: easeOutExpo }}
                className="-mt-3 flex flex-col gap-5 overflow-hidden"
              >
                <Field label="Games" hint="The ones this level allows">
                  <ul className="flex flex-col gap-3">
                    {(Object.keys(g.enabled) as GameKind[]).map((k) => (
                      <li key={k}>
                        <Toggle
                          label={GAMES[k].label}
                          hint={GAMES[k].hint}
                          checked={g.enabled[k]}
                          onChange={(on) => tune({ enabled: { ...g.enabled, [k]: on } })}
                        />
                      </li>
                    ))}
                  </ul>
                </Field>
                <Field label="How often" hint={g.frequency === 0 ? 'Only when someone starts a round' : `A round about every ${g.frequency === 1 ? 'song' : `${g.frequency} songs`}`}>
                  <ToggleGroup
                    type="single"
                    value={String(g.frequency)}
                    onValueChange={(v) => v && tune({ frequency: Number(v) })}
                    aria-label="How often"
                    className="flex-wrap"
                  >
                    {(FREQUENCIES.includes(g.frequency) ? FREQUENCIES : [...FREQUENCIES, g.frequency].sort((a, b) => a - b)).map((n) => (
                      <ToggleGroupItem key={n} value={String(n)} className="min-w-11">
                        {n === 0 ? 'By hand' : n}
                      </ToggleGroupItem>
                    ))}
                  </ToggleGroup>
                </Field>
                <Field label="Start a round" hint="Anytime, from the room menu">
                  <ToggleGroup
                    type="single"
                    value={room.permissions.startRounds}
                    onValueChange={(v) => v && onStartRounds(v as Room['permissions']['startRounds'])}
                    aria-label="Who can start a round"
                  >
                    <ToggleGroupItem value="everyone">Everyone</ToggleGroupItem>
                    <ToggleGroupItem value="owner">{ownerLabel}</ToggleGroupItem>
                  </ToggleGroup>
                </Field>
                <Field label="Scores" hint={SCORES.find((s) => s.id === g.scores)?.hint}>
                  <ToggleGroup type="single" value={g.scores} onValueChange={(v) => v && tune({ scores: v as RoomGames['scores'] })} aria-label="Scores">
                    {SCORES.map((s) => (
                      <ToggleGroupItem key={s.id} value={s.id}>
                        {s.label}
                      </ToggleGroupItem>
                    ))}
                  </ToggleGroup>
                </Field>
                <Toggle
                  label="Guests can play"
                  hint="Separate from their hearts and votes"
                  checked={g.guests}
                  onChange={(guests) => tune({ guests })}
                />
                <Toggle
                  label="Big screen only"
                  hint="Rounds show on the TV, never as prompts on phones"
                  checked={g.tvOnly}
                  onChange={(tvOnly) => tune({ tvOnly })}
                />
                {g.level === 'gamenight' && (
                  <Field label="Breaks per hour" hint="The most rounds an hour that may pause the music">
                    <ToggleGroup
                      type="single"
                      value={String(g.breaksPerHour)}
                      onValueChange={(v) => v && tune({ breaksPerHour: Number(v) })}
                      aria-label="Breaks per hour"
                    >
                      {(BREAKS.includes(g.breaksPerHour) ? BREAKS : [...BREAKS, g.breaksPerHour].sort((a, b) => a - b)).map((n) => (
                        <ToggleGroupItem key={n} value={String(n)} className="min-w-11">
                          {n === 0 ? 'None' : n}
                        </ToggleGroupItem>
                      ))}
                    </ToggleGroup>
                  </Field>
                )}
              </motion.div>
            )}
          </AnimatePresence>
          <Toggle
            label="Mute games on this device"
            hint="Rounds won’t pop up here. Remembered on this device only"
            checked={muted}
            onChange={setGamesMuted}
          />
        </div>
      )}
    </>
  )
}

function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <div>
        <p className="text-sm font-medium">{label}</p>
        {hint && <p className="text-caption text-muted-foreground">{hint}</p>}
      </div>
      {children}
    </div>
  )
}

function Toggle({ label, hint, checked, onChange }: { label: string; hint: string; checked: boolean; onChange: (on: boolean) => void }) {
  return (
    <label className="flex cursor-pointer items-start justify-between gap-4">
      <span>
        <span className="block text-sm font-medium">{label}</span>
        <span className="block text-caption text-muted-foreground">{hint}</span>
      </span>
      <Switch checked={checked} onChange={onChange} label={label} />
    </label>
  )
}
