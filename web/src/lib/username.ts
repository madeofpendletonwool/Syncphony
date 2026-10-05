/** Usernames the server accepts (see `Username` in api/openapi.yaml). */
export const USERNAME_PATTERN = /^[a-z0-9._-]{2,32}$/

/** A username to suggest from a display name: "Zoë Q. Smith" → "zoe.q.smith". */
export function suggestUsername(displayName: string) {
  return displayName
    .normalize('NFKD')
    .replace(/\p{M}/gu, '')
    .toLowerCase()
    .replace(/[\s_-]+/g, '.')
    .replace(/[^a-z0-9.]/g, '')
    .replace(/\.{2,}/g, '.')
    .replace(/^\.+|\.+$/g, '')
    .slice(0, 32)
    .replace(/\.+$/, '')
}

/** Why a username won't be accepted, or undefined if it's fine. */
export function usernameProblem(username: string) {
  if (username.length < 2) return 'At least 2 characters.'
  if (username.length > 32) return 'At most 32 characters.'
  if (!USERNAME_PATTERN.test(username)) return 'Only lowercase letters, numbers, and . _ -'
  return undefined
}
