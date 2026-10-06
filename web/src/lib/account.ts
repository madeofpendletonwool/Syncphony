import { queryOptions, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { meQuery, rememberMe, type Me } from './auth'
import { usersQuery } from './users'

export type Passkey = components['schemas']['Passkey']
export type SignedInSession = components['schemas']['SignedInSession']

export const passkeysQuery = queryOptions({
  queryKey: ['me', 'passkeys'],
  queryFn: () => unwrap(api.GET('/me/passkeys')),
})

export const mySessionsQuery = queryOptions({
  queryKey: ['me', 'sessions'],
  queryFn: () => unwrap(api.GET('/me/sessions')),
})

/**
 * Stores an updated `me` from the server, and refreshes the user list so
 * avatars and names elsewhere catch up.
 */
export function useSetMe() {
  const queryClient = useQueryClient()
  return (me: Me) => {
    rememberMe(me)
    queryClient.setQueryData(meQuery.queryKey, me)
    void queryClient.invalidateQueries({ queryKey: usersQuery.queryKey })
  }
}

/** Refetches `me`, for changes that only alter its credential counts. */
export function useRefreshMe() {
  const queryClient = useQueryClient()
  return () => queryClient.invalidateQueries({ queryKey: meQuery.queryKey, exact: true })
}

export type DeviceKind = 'phone' | 'tablet' | 'computer' | 'unknown'

/** A readable name for a signed-in device, like "Safari on iPhone". */
export function describeDevice(ua: string): { label: string; kind: DeviceKind } {
  const os = osOf(ua)
  const browser = browserOf(ua)
  if (!os && !browser) return { label: 'Unknown device', kind: 'unknown' }
  const label = browser && os ? `${browser} on ${os.name}` : (browser ?? os!.name)
  return { label, kind: os?.kind ?? 'unknown' }
}

function osOf(ua: string): { name: string; kind: DeviceKind } | undefined {
  if (/iPhone/.test(ua)) return { name: 'iPhone', kind: 'phone' }
  if (/iPad/.test(ua)) return { name: 'iPad', kind: 'tablet' }
  if (/Android/.test(ua)) return { name: 'Android', kind: /Mobile/.test(ua) ? 'phone' : 'tablet' }
  if (/CrOS/.test(ua)) return { name: 'ChromeOS', kind: 'computer' }
  if (/Macintosh|Mac OS X/.test(ua)) return { name: 'Mac', kind: 'computer' }
  if (/Windows/.test(ua)) return { name: 'Windows', kind: 'computer' }
  if (/Linux/.test(ua)) return { name: 'Linux', kind: 'computer' }
  return undefined
}

function browserOf(ua: string): string | undefined {
  // Order matters: most browsers also claim to be Chrome and Safari.
  if (/Edg(e|A|iOS)?\//.test(ua)) return 'Edge'
  if (/OPR\/|Opera/.test(ua)) return 'Opera'
  if (/SamsungBrowser\//.test(ua)) return 'Samsung Internet'
  if (/Firefox\/|FxiOS\//.test(ua)) return 'Firefox'
  if (/Chrome\/|CriOS\//.test(ua)) return 'Chrome'
  if (/Safari\//.test(ua) && /Version\//.test(ua)) return 'Safari'
  // Home Screen apps on iOS leave "Safari" out.
  if (/AppleWebKit\//.test(ua) && /iPhone|iPad/.test(ua)) return 'Safari'
  return undefined
}
