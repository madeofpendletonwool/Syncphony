import { Check } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

/** An artist to pick, with a line about why it's offered. */
export type Pickable = { name: string; detail?: string; disabled?: boolean }

/**
 * A checklist of artists to pick from: nothing joins a set unless you
 * check it (library ≠ taste).
 */
export function PickList({
  items,
  picked,
  onChange,
  label,
}: {
  items: readonly Pickable[]
  picked: ReadonlySet<string>
  onChange: (picked: Set<string>) => void
  label: string
}) {
  const open = items.filter((it) => !it.disabled)
  const all = open.length > 0 && open.every((it) => picked.has(it.name))
  const toggle = (name: string) => {
    const next = new Set(picked)
    if (next.has(name)) next.delete(name)
    else next.add(name)
    onChange(next)
  }
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between px-1">
        <span className="text-caption text-muted-foreground">
          {picked.size} of {open.length} picked
        </span>
        <Button variant="ghost" size="sm" disabled={open.length === 0} onClick={() => onChange(all ? new Set() : new Set(open.map((it) => it.name)))}>
          {all ? 'None' : 'All'}
        </Button>
      </div>
      <ul aria-label={label} className="-mx-1 flex max-h-[45dvh] flex-col overflow-y-auto">
        {items.map((it) => {
          const on = picked.has(it.name)
          return (
            <li key={it.name}>
              <button
                type="button"
                role="checkbox"
                aria-checked={on}
                disabled={it.disabled}
                onClick={() => toggle(it.name)}
                className="flex w-full items-center gap-3 rounded-2xl p-2 text-left outline-none hover:bg-foreground/5 focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50"
              >
                <span
                  className={cn(
                    'grid size-6 shrink-0 place-items-center rounded-lg border transition-colors',
                    on ? 'border-primary bg-primary text-primary-foreground' : 'border-foreground/25',
                  )}
                >
                  {on && <Check className="size-4" />}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-medium">{it.name}</span>
                  {it.detail && <span className="block truncate text-caption text-muted-foreground">{it.detail}</span>}
                </span>
              </button>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
