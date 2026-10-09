import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { ListMusic, LoaderCircle } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { errorMessage } from '@/api/errors'
import { Field } from '@/components/field'
import { Sheet } from '@/components/sheet'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { tap } from '@/lib/haptics'
import { nightPlaylistName, playlistChanged, saveNight } from '@/lib/playlists'
import { toast } from '@/lib/toast'

/** A night: the room it played in, and when, as a recap's range. */
export type NightRange = { roomId: string; roomName: string; from: string; to: string }

/**
 * Saves the songs a night played as a Syncphony playlist: in the order
 * they played, each keeping who brought it and the service it came from.
 */
export function SaveNightSheet({ night, open, onOpenChange }: { night: NightRange; open: boolean; onOpenChange: (open: boolean) => void }) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [name, setName] = useState(() => nightPlaylistName(night.roomName, night.from))
  const [keepSkipped, setKeepSkipped] = useState(false)
  const [keepRepeats, setKeepRepeats] = useState(false)
  const [share, setShare] = useState(true)
  const save = useMutation({
    mutationFn: () => saveNight(night.roomId, { name: name.trim(), from: night.from, to: night.to, keepSkipped, keepRepeats, share }),
    onMutate: () => tap(),
    onSuccess: (p) => {
      playlistChanged(qc, p)
      void qc.invalidateQueries({ queryKey: ['recap', night.roomId] })
      onOpenChange(false)
      toast({
        message: `Saved ${p.songs.length} songs to ${p.name}`,
        action: { label: 'Open', onClick: () => void navigate({ to: '/library/$playlistId', params: { playlistId: p.id } }) },
      })
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })

  return (
    <Sheet open={open} onOpenChange={onOpenChange} title="Save tonight's playlist" description="Every song that played, in order, from everyone's services.">
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          if (name.trim()) save.mutate()
        }}
      >
        <Field label="Name" value={name} onChange={(e) => setName(e.target.value)} maxLength={100} />
        <Toggle label="Keep skipped songs" help="Songs the room skipped stay out unless you keep them." checked={keepSkipped} onChange={setKeepSkipped} />
        <Toggle label="Keep repeats" help="A song that played twice is in it twice." checked={keepRepeats} onChange={setKeepRepeats} />
        <Toggle label={`Share with ${night.roomName}`} help="Everyone in the room can play it again. Only you can change it." checked={share} onChange={setShare} />
        <Button type="submit" size="lg" disabled={!name.trim() || save.isPending}>
          {save.isPending ? <LoaderCircle className="animate-spin" data-icon="inline-start" /> : <ListMusic data-icon="inline-start" />}
          Save playlist
        </Button>
      </form>
    </Sheet>
  )
}

function Toggle({ label, help, checked, onChange }: { label: string; help: ReactNode; checked: boolean; onChange: (on: boolean) => void }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <div className="min-w-0">
        <p className="font-medium">{label}</p>
        <p className="text-sm text-muted-foreground">{help}</p>
      </div>
      <Switch checked={checked} onChange={onChange} label={label} />
    </div>
  )
}

/** A button that opens SaveNightSheet. */
export function SaveNightButton({ night, children, ...props }: { night: NightRange; children?: ReactNode } & Omit<React.ComponentProps<typeof Button>, 'onClick'>) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button {...props} onClick={() => setOpen(true)}>
        {children ?? (
          <>
            <ListMusic data-icon="inline-start" />
            Save as a playlist
          </>
        )}
      </Button>
      <SaveNightSheet key={night.from} night={night} open={open} onOpenChange={setOpen} />
    </>
  )
}
