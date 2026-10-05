import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'

/** Everyone on the server, for names, avatars and lane colors. */
export const usersQuery = queryOptions({
  queryKey: ['users'],
  queryFn: () => unwrap(api.GET('/users')),
  staleTime: 5 * 60_000,
})
