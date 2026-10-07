import { queryOptions, useQuery } from '@tanstack/react-query'
import { useEffect } from 'react'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'

/** The server's own settings: its name, and how long invites last. */
export const serverSettingsQuery = queryOptions({
  queryKey: ['server-settings'],
  queryFn: () => unwrap(api.GET('/server-settings')),
  staleTime: 5 * 60_000,
})

/** "Syncphony", or "The Den · Syncphony" when the server has a name. */
export function appTitle(instanceName: string | undefined) {
  return instanceName ? `${instanceName} · Syncphony` : 'Syncphony'
}

/** Names the browser tab, and the Home Screen app's switcher, after the server. */
export function useServerTitle() {
  const name = useQuery(serverSettingsQuery).data?.instanceName
  useEffect(() => {
    document.title = appTitle(name)
  }, [name])
}

const sizes = ['bytes', 'KB', 'MB', 'GB', 'TB']

/** "812 KB", "1.4 GB". */
export function formatBytes(n: number) {
  let i = 0
  while (n >= 1024 && i < sizes.length - 1) {
    n /= 1024
    i++
  }
  return `${i === 0 || n >= 10 ? Math.round(n) : n.toFixed(1)} ${sizes[i]}`
}
