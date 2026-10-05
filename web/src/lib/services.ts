import { queryOptions } from '@tanstack/react-query'
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
