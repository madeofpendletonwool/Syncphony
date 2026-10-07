import { useQuery } from '@tanstack/react-query'
import type { components } from '@/api/schema.gen'
import { useMe } from '@/lib/auth'
import { linkNames, providersQuery, sourceName } from '@/lib/services'
import { usersQuery } from '@/lib/users'

type ServiceLink = components['schemas']['ServiceLink']

/** What to call each link: "Navidrome", "Sam's Navidrome", or with its account when two match. */
export function useLinkNames(links: ServiceLink[]) {
  const me = useMe()
  const providers = useQuery(providersQuery)
  const users = useQuery(usersQuery)
  return linkNames(links, (l) =>
    sourceName(
      providers.data?.find((p) => p.id === l.provider)?.name ?? l.provider,
      users.data?.find((u) => u.id === l.ownerId)?.displayName,
      l.ownerId === me.id,
    ),
  )
}
