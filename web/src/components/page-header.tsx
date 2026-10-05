import type { ReactNode } from 'react'

/** Big page title with an optional supporting line and trailing actions. */
export function PageHeader({ title, subtitle, actions }: { title: string; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <header className="flex items-end justify-between gap-4 pt-10 pb-6">
      <div className="min-w-0">
        <h1 className="text-display">{title}</h1>
        {subtitle && <p className="mt-2 text-muted-foreground">{subtitle}</p>}
      </div>
      {actions}
    </header>
  )
}
