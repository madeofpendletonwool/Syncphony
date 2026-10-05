import { Eye, EyeOff } from 'lucide-react'
import { useId, useState, type ComponentProps, type ReactNode } from 'react'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

type FieldProps = ComponentProps<typeof Input> & {
  label: string
  help?: ReactNode
  error?: string
  /** Masks the value, with a button to reveal it. */
  secret?: boolean
}

/** A labelled input with help text and an inline error. */
export function Field({ label, help, error, secret, className, id, type, ...props }: FieldProps) {
  const auto = useId()
  const inputId = id ?? auto
  const describedBy = `${inputId}-desc`
  const [shown, setShown] = useState(false)

  return (
    <div className={cn('flex flex-col gap-1.5', className)}>
      <label htmlFor={inputId} className="px-1 text-sm font-medium">
        {label}
      </label>
      <div className="relative">
        <Input
          id={inputId}
          type={secret ? (shown ? 'text' : 'password') : type}
          aria-invalid={error ? true : undefined}
          aria-describedby={help || error ? describedBy : undefined}
          className={cn(secret && 'pr-12')}
          {...props}
        />
        {secret && (
          <button
            type="button"
            onClick={() => setShown((s) => !s)}
            aria-label={shown ? `Hide ${label.toLowerCase()}` : `Show ${label.toLowerCase()}`}
            aria-pressed={shown}
            className="absolute top-1/2 right-1.5 grid size-8 -translate-y-1/2 place-items-center rounded-lg text-muted-foreground transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            {shown ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
          </button>
        )}
      </div>
      {(error || help) && (
        <p id={describedBy} className={cn('px-1 text-caption', error ? 'text-destructive' : 'text-muted-foreground')}>
          {error ?? help}
        </p>
      )}
    </div>
  )
}
