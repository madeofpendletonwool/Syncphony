import { LoaderCircle } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { Button } from '@/components/ui/button'

/** A button that asks for a second tap before doing something destructive. */
export function ConfirmButton({
  onConfirm,
  pending,
  disabled,
  label,
  confirmLabel,
  icon,
  compact,
  size = 'sm',
  className,
}: {
  onConfirm: () => void
  pending?: boolean
  disabled?: boolean
  label: string
  confirmLabel: string
  icon: ReactNode
  /** Just the icon until armed; label becomes its accessible name. */
  compact?: boolean
  size?: 'sm' | 'default'
  className?: string
}) {
  const [armed, setArmed] = useState(false)
  useEffect(() => {
    if (!armed) return
    const t = setTimeout(() => setArmed(false), 4000)
    return () => clearTimeout(t)
  }, [armed])
  const iconOnly = compact && !armed
  return (
    <Button
      variant={armed ? 'destructive' : 'ghost'}
      size={iconOnly ? 'icon-sm' : size}
      aria-label={iconOnly ? label : undefined}
      disabled={disabled || pending}
      className={className}
      onClick={() => {
        if (!armed) return setArmed(true)
        setArmed(false)
        onConfirm()
      }}
    >
      {pending ? <LoaderCircle className="animate-spin" /> : icon}
      {iconOnly ? null : armed ? confirmLabel : label}
    </Button>
  )
}
