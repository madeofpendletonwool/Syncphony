import { queryOptions, useQueryClient, useSuspenseQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { api } from '@/api/client'
import { ApiError, unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'

export type Me = components['schemas']['Me']

/** The signed-in user, or null when signed out. */
export const meQuery = queryOptions({
  queryKey: ['me'],
  queryFn: async (): Promise<Me | null> => {
    try {
      return await unwrap(api.GET('/me'))
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) return null
      throw err
    }
  },
  staleTime: 60_000,
})

/**
 * The signed-in user. Only call it under the signed-in layout, which makes
 * sure there is one before rendering.
 */
export function useMe(): Me {
  const me = useSuspenseQuery(meQuery).data
  if (!me) throw new Error('useMe called while signed out')
  return me
}

/** Where to go after signing in: a same-origin path, never a full URL. */
export function safeRedirect(to: unknown, fallback = '/room') {
  if (typeof to !== 'string' || !to.startsWith('/') || to.startsWith('//') || to.startsWith('/\\')) {
    return fallback
  }
  return to
}

/** Signs out, drops every cached query, and goes to the sign-in page. */
export function useSignOut() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  return async () => {
    try {
      await unwrap(api.POST('/auth/logout'))
    } finally {
      // The signed-in layout leaves as soon as `me` is null; drop the rest of
      // the cache once its screens are gone.
      queryClient.setQueryData(meQuery.queryKey, null)
      await navigate({ to: '/login', replace: true })
      queryClient.removeQueries({ predicate: (q) => q.queryKey[0] !== meQuery.queryKey[0] })
    }
  }
}
