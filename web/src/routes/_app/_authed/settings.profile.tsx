import { useMutation } from '@tanstack/react-query'
import { createFileRoute, redirect } from '@tanstack/react-router'
import { Camera, Check, LoaderCircle, Pipette, Trash2 } from 'lucide-react'
import { motion } from 'motion/react'
import { useEffect, useEffectEvent, useRef, useState, type FormEvent } from 'react'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { Field } from '@/components/field'
import { Notice } from '@/components/notice'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { UserAvatar } from '@/components/user-avatar'
import { useSetMe } from '@/lib/account'
import { meQuery, useMe } from '@/lib/auth'
import { LANE_PALETTE, laneStyle } from '@/lib/lane'
import { fadeUp, spring, stagger } from '@/lib/motion'
import { cn } from '@/lib/utils'

type ProfileUpdate = components['schemas']['ProfileUpdate']

export const Route = createFileRoute('/_app/_authed/settings/profile')({
  beforeLoad: async ({ context }) => {
    const me = await context.queryClient.ensureQueryData(meQuery)
    if (me?.guest) throw redirect({ to: '/me', replace: true })
  },
  component: Profile,
})

/** The server's upload limit (server/internal/avatar). */
const MAX_UPLOAD = 10 << 20

function Profile() {
  return (
    <>
      <PageHeader title="Profile" subtitle="How you show up in the queue and to everyone in the room." />
      <motion.div variants={stagger} initial="hidden" animate="show" className="flex flex-col gap-4">
        <Picture />
        <DisplayName />
        <LaneColor />
      </motion.div>
    </>
  )
}

/** Saves profile fields and stores the result. */
function useUpdateProfile() {
  const setMe = useSetMe()
  return useMutation({
    mutationFn: (body: ProfileUpdate) => unwrap(api.PATCH('/me', { body })),
    onSuccess: setMe,
  })
}

function Picture() {
  const me = useMe()
  const setMe = useSetMe()
  const input = useRef<HTMLInputElement>(null)
  const [tooBig, setTooBig] = useState(false)

  const upload = useMutation({
    mutationFn: (file: File) =>
      unwrap(
        api.PUT('/me/avatar', {
          // The body is the file itself, not JSON.
          body: file as unknown as string,
          bodySerializer: (b) => b,
          headers: { 'Content-Type': file.type || 'application/octet-stream' },
        }),
      ),
    onSuccess: setMe,
  })
  const remove = useUpdateProfile()
  const busy = upload.isPending || remove.isPending

  const choose = (file: File | undefined) => {
    if (!file) return
    upload.reset()
    remove.reset()
    setTooBig(file.size > MAX_UPLOAD)
    if (file.size <= MAX_UPLOAD) upload.mutate(file)
  }

  const error = tooBig ? 'That picture is over 10 MB. Try a smaller one.' : (upload.error ?? remove.error)

  return (
    <motion.section variants={fadeUp} className="glass flex flex-col items-center gap-4 rounded-3xl p-6 text-center">
      <div className="relative">
        <UserAvatar user={me} ring className="size-28 text-3xl" />
        {busy && (
          <span className="absolute inset-0 grid place-items-center rounded-full bg-background/60 backdrop-blur-sm">
            <LoaderCircle className="size-7 animate-spin text-primary" />
          </span>
        )}
        <Button
          size="icon"
          aria-label={me.avatar ? 'Change picture' : 'Add a picture'}
          onClick={() => input.current?.click()}
          disabled={busy}
          className="absolute -right-1 -bottom-1 ring-4 ring-background"
        >
          <Camera />
        </Button>
      </div>
      <input
        ref={input}
        type="file"
        accept="image/jpeg,image/png,image/gif,image/webp"
        className="sr-only"
        tabIndex={-1}
        aria-hidden
        onChange={(e) => {
          choose(e.target.files?.[0])
          e.target.value = ''
        }}
      />
      <div>
        <h2 className="text-headline">Picture</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          {me.avatar ? 'Cropped to a circle for everyone to see.' : 'Without one, people see your initials on your color.'}
        </p>
      </div>
      {me.avatar && (
        <Button variant="ghost" size="sm" onClick={() => remove.mutate({ avatar: '' })} disabled={busy}>
          <Trash2 data-icon="inline-start" />
          Use initials instead
        </Button>
      )}
      <Notice className="w-full text-left">{error && (typeof error === 'string' ? error : errorMessage(error))}</Notice>
    </motion.section>
  )
}

function DisplayName() {
  const me = useMe()
  const [name, setName] = useState(me.displayName)
  const [saved, setSaved] = useState(false)
  const update = useUpdateProfile()
  const trimmed = name.trim()
  const changed = trimmed !== me.displayName

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!changed || !trimmed) return
    update.mutate(
      { displayName: trimmed },
      {
        onSuccess: (m) => {
          setName(m.displayName)
          setSaved(true)
          setTimeout(() => setSaved(false), 1800)
        },
      },
    )
  }

  return (
    <motion.form variants={fadeUp} onSubmit={submit} className="glass flex flex-col gap-4 rounded-3xl p-5">
      <Field
        label="Display name"
        value={name}
        maxLength={64}
        autoComplete="nickname"
        onChange={(e) => {
          setName(e.target.value)
          setSaved(false)
        }}
        help={`Signs in as @${me.username}; that can't change.`}
        error={trimmed ? undefined : 'Your name can’t be empty.'}
      />
      <Notice>{update.error && errorMessage(update.error)}</Notice>
      <Button type="submit" className="self-end" disabled={!changed || !trimmed || update.isPending}>
        {update.isPending ? <LoaderCircle className="animate-spin" /> : saved ? <Check data-icon="inline-start" /> : null}
        {saved ? 'Saved' : 'Save'}
      </Button>
    </motion.form>
  )
}

function LaneColor() {
  const me = useMe()
  const update = useUpdateProfile()
  // Shown right away; the server confirms.
  const color = update.isPending ? (update.variables.color ?? me.color) : me.color
  const custom = !(LANE_PALETTE as readonly string[]).includes(color)
  const pick = (c: string) => c.toLowerCase() !== me.color && update.mutate({ color: c.toLowerCase() })

  return (
    <motion.section variants={fadeUp} className="glass flex flex-col rounded-3xl p-5">
      <h2 className="text-headline">Your color</h2>
      <p className="mt-1 text-sm text-muted-foreground">Marks your songs in the queue, and fills in behind your initials.</p>
      <div role="radiogroup" aria-label="Lane color" className="mt-4 grid grid-cols-6 gap-3 sm:grid-cols-7">
        {LANE_PALETTE.map((c) => (
          <Swatch key={c} color={c} selected={color === c} onSelect={() => pick(c)} />
        ))}
        <CustomColor color={color} selected={custom} onPick={pick} />
      </div>
      <Notice className="mt-4">{update.error && errorMessage(update.error)}</Notice>
    </motion.section>
  )
}

/** Any color, from the system picker. Saves when the picker closes, not while dragging. */
function CustomColor({ color, selected, onPick }: { color: string; selected: boolean; onPick: (c: string) => void }) {
  const input = useRef<HTMLInputElement>(null)
  const [draft, setDraft] = useState<string>()
  const shown = draft ?? (selected ? color : undefined)
  const pick = useEffectEvent((c: string) => {
    setDraft(undefined)
    onPick(c)
  })

  useEffect(() => {
    const el = input.current
    if (!el) return
    // React's onChange fires on every input; the native change event fires once.
    const commit = () => pick(el.value)
    el.addEventListener('change', commit)
    return () => el.removeEventListener('change', commit)
  }, [])

  return (
    <label
      style={laneStyle(shown)}
      className={cn(
        'relative grid aspect-square cursor-pointer place-items-center rounded-full text-white transition-transform outline-none focus-within:ring-3 focus-within:ring-ring/50 active:scale-95',
        shown
          ? 'bg-(--lane) ring-2 ring-(--lane) ring-offset-2 ring-offset-background'
          : 'bg-[conic-gradient(from_0deg,#f43f5e,#eab308,#10b981,#0ea5e9,#7c3aed,#f43f5e)]',
      )}
    >
      <Pipette className="size-4 drop-shadow" />
      <input
        ref={input}
        type="color"
        aria-label="Pick any color"
        value={shown ?? color}
        onChange={(e) => setDraft(e.target.value)}
        className="absolute inset-0 cursor-pointer opacity-0"
      />
    </label>
  )
}

function Swatch({ color, selected, onSelect }: { color: string; selected: boolean; onSelect: () => void }) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      aria-label={color}
      onClick={onSelect}
      style={laneStyle(color)}
      className={cn(
        'grid aspect-square place-items-center rounded-full bg-(--lane) text-white transition-transform outline-none focus-visible:ring-3 focus-visible:ring-ring/50 active:scale-95',
        selected && 'ring-2 ring-(--lane) ring-offset-2 ring-offset-background',
      )}
    >
      {selected && (
        <motion.span initial={{ scale: 0 }} animate={{ scale: 1 }} transition={spring}>
          <Check className="size-4" strokeWidth={3} />
        </motion.span>
      )}
    </button>
  )
}
