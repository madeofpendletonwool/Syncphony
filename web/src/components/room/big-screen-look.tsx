import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Slider } from '@/components/ui/slider'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { SHOWS } from '@/hooks/use-scene'
import type { ChatLook } from '@/lib/chat'
import { roomsQuery, type Room } from '@/lib/room'
import { toast } from '@/lib/toast'

type Screens = Room['screens']

const looks: { value: Screens['look']; label: string; hint: string }[] = [
  { value: 'auto', label: 'Auto', hint: 'Lyrics and what’s next, with the visualizer for songs with no words.' },
  { value: 'stage', label: 'Stage', hint: 'Now playing, lyrics and what’s next.' },
  { value: 'visualizer', label: 'Visualizer', hint: 'Full-screen visuals, with what’s playing in a corner.' },
]

const chatLooks: { value: ChatLook; label: string; hint: string }[] = [
  { value: 'floating', label: 'Floating', hint: 'Messages drift across the top of the screen, then fade.' },
  { value: 'log', label: 'Side log', hint: 'The last few messages in a corner.' },
  { value: 'off', label: 'Off', hint: 'The room’s chat stays on phones.' },
]

/**
 * How the room's big screens show the music (MAD-779). Every screen
 * follows; whoever manages the room changes it.
 */
export function BigScreenLook({ room, editable }: { room: Room; editable: boolean }) {
  const queryClient = useQueryClient()
  const [intensity, setIntensity] = useState<number>()
  const update = useMutation({
    mutationFn: (screens: Screens) =>
      unwrap(api.PATCH('/rooms/{roomId}', { params: { path: { roomId: room.id } }, body: { screens } })),
    onMutate: (screens) => {
      const before = queryClient.getQueryData(roomsQuery.queryKey)
      queryClient.setQueryData(roomsQuery.queryKey, (rs) => rs?.map((r) => (r.id === room.id ? { ...r, screens } : r)))
      return before
    },
    onError: (e, _, before) => {
      queryClient.setQueryData(roomsQuery.queryKey, before)
      toast({ message: errorMessage(e), tone: 'error' })
    },
    onSettled: () => setIntensity(undefined),
  })
  const s = room.screens
  const set = (next: Partial<Screens>) => update.mutate({ ...s, ...next })
  const shown = intensity ?? s.intensity
  const chat = s.chat ?? 'floating'

  return (
    <section className="flex flex-col gap-3">
      <h3 className="text-caption font-medium tracking-wide text-muted-foreground uppercase">How screens look</h3>
      <ToggleGroup
        type="single"
        value={s.look}
        disabled={!editable}
        onValueChange={(v) => v && set({ look: v as Screens['look'] })}
        className="grid w-full grid-cols-3"
      >
        {looks.map((l) => (
          <ToggleGroupItem key={l.value} value={l.value}>
            {l.label}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
      <p className="-mt-1 text-caption text-muted-foreground">{looks.find((l) => l.value === s.look)?.hint}</p>

      <div className="flex flex-col gap-2">
        <span className="text-sm text-muted-foreground">Visualizer</span>
        <div className="flex flex-wrap gap-1.5">
          {['', ...SHOWS].map((show) => (
            <Button
              key={show || 'auto'}
              size="xs"
              variant={s.scene === show ? 'default' : 'secondary'}
              disabled={!editable}
              className="capitalize"
              onClick={() => set({ scene: show })}
            >
              {show || 'Suits the song'}
            </Button>
          ))}
        </div>
      </div>

      <label className="flex flex-col gap-2">
        <span className="flex justify-between text-sm text-muted-foreground">
          Intensity <span className="text-foreground tabular-nums">{Math.round(shown * 100)}%</span>
        </span>
        <Slider
          min={0.5}
          max={2}
          step={0.05}
          disabled={!editable}
          value={[shown]}
          onValueChange={([v]) => setIntensity(v)}
          onValueCommit={([v]) => set({ intensity: v })}
        />
      </label>
      <div className="flex flex-col gap-2">
        <span className="text-sm text-muted-foreground">Chat on the big screen</span>
        <ToggleGroup
          type="single"
          value={chat}
          disabled={!editable}
          onValueChange={(v) => v && set({ chat: v as ChatLook })}
          aria-label="Chat on the big screen"
          className="grid w-full grid-cols-3"
        >
          {chatLooks.map((l) => (
            <ToggleGroupItem key={l.value} value={l.value}>
              {l.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <p className="text-caption text-muted-foreground">{chatLooks.find((l) => l.value === chat)?.hint}</p>
      </div>
      {!editable && <p className="text-caption text-muted-foreground">Whoever runs the room can change these.</p>}
    </section>
  )
}
