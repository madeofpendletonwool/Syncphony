import { queryOptions, useQuery } from '@tanstack/react-query'
import { AudioLines, FlaskConical, Music, Server } from 'lucide-react'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'

/** Services this server can link. They only change on a server restart. */
export const providersQuery = queryOptions({
  queryKey: ['providers'],
  queryFn: () => unwrap(api.GET('/providers')),
  staleTime: Infinity,
})

/** Your linked accounts and their health. */
export const linksQuery = queryOptions({
  queryKey: ['links'],
  queryFn: () => unwrap(api.GET('/links')),
})

// Keyed by ProviderInfo.icon. Unknown providers get a generic note.
const icons: Record<string, { icon: typeof Music; color: string }> = {
  navidrome: { icon: Server, color: '#3b82f6' },
  spotify: { icon: AudioLines, color: '#1db954' },
  fake: { icon: FlaskConical, color: '#a855f7' },
}

/** A provider's glyph and brand color, by ProviderInfo.icon. */
export function providerLook(icon: string | undefined) {
  return (icon && icons[icon]) || { icon: Music, color: 'var(--primary)' }
}

/** A service's display name and look, by provider ID. */
export function useService(provider: string) {
  const info = useQuery(providersQuery).data?.find((p) => p.id === provider)
  return { name: info?.name ?? provider, ...providerLook(info?.icon ?? provider) }
}
