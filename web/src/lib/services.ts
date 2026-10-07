import { queryOptions, useQuery } from '@tanstack/react-query'
import { AudioLines, FlaskConical, Music, Server, Ticket } from 'lucide-react'
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

/** Every link you can search and queue from: yours, then others' shared ones. */
export const usableLinksQuery = queryOptions({
  queryKey: ['links', 'usable'],
  queryFn: () => unwrap(api.GET('/links', { params: { query: { include: 'shared' } } })),
})

/** How to name a link's source: "Navidrome", or "Sam's Navidrome" when it's shared with you. */
export function sourceName(providerName: string, ownerName: string | undefined, mine: boolean) {
  if (mine || !ownerName) return providerName
  return `${ownerName.split(' ')[0]}'s ${providerName}`
}

/**
 * Names for several links at once, from nameOf (usually sourceName). Links
 * that would read the same, like two Navidromes of your own, get their
 * account added: "Navidrome · alice on music.example.com".
 */
export function linkNames<L extends { id: string; accountLabel: string }>(links: L[], nameOf: (link: L) => string) {
  const base = links.map(nameOf)
  return new Map(
    links.map((l, i) => [l.id, base.indexOf(base[i]) !== base.lastIndexOf(base[i]) ? `${base[i]} · ${l.accountLabel}` : base[i]]),
  )
}

// Keyed by ProviderInfo.icon. Unknown providers get a generic note.
const icons: Record<string, { icon: typeof Music; color: string }> = {
  navidrome: { icon: Server, color: '#3b82f6' },
  spotify: { icon: AudioLines, color: '#1db954' },
  nugs: { icon: Ticket, color: '#e8590c' },
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
