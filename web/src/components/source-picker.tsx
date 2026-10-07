import { useQuery } from '@tanstack/react-query'
import type { components } from '@/api/schema.gen'
import { ProviderIcon } from '@/components/provider-icon'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { useLinkNames } from '@/hooks/use-link-names'
import { providersQuery } from '@/lib/services'
import { cn } from '@/lib/utils'

type ServiceLink = components['schemas']['ServiceLink']

const ALL = 'all'

/**
 * Chips to narrow Search to one account, or "All services". Nothing to pick
 * with a single account, so then there's no row.
 */
export function SourcePicker({
  links,
  value,
  onChange,
  className,
}: {
  links: ServiceLink[]
  value: string | undefined
  onChange: (linkId: string | undefined) => void
  className?: string
}) {
  const providers = useQuery(providersQuery)
  const names = useLinkNames(links)
  if (links.length < 2) return null
  return (
    <ToggleGroup
      type="single"
      value={value ?? ALL}
      onValueChange={(v) => v && onChange(v === ALL ? undefined : v)}
      aria-label="Search in"
      className={cn('glass max-w-full self-start overflow-x-auto [scrollbar-width:none]', className)}
    >
      <ToggleGroupItem value={ALL} className="shrink-0">
        All services
      </ToggleGroupItem>
      {links.map((l) => (
        <ToggleGroupItem key={l.id} value={l.id} className="shrink-0 pl-2 [&_svg]:size-2.5">
          <ProviderIcon icon={providers.data?.find((p) => p.id === l.provider)?.icon ?? l.provider} className="size-4 rounded-full" />
          {names.get(l.id)}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}
