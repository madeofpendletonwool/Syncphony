import type { components } from './schema.gen'

type ErrorBody = components['schemas']['Error']

/** A failed API call, with the server's stable error code. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  /** Seconds to wait, from Retry-After on 429s. */
  readonly retryAfter?: number

  constructor(status: number, code: string, message: string, retryAfter?: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.retryAfter = retryAfter
  }
}

type Result<T> = { data?: T; error?: ErrorBody | unknown; response: Response }

/**
 * Resolves an openapi-fetch call to its data, or throws an ApiError.
 * Use it inside query and mutation functions.
 */
export async function unwrap<T>(call: Promise<Result<T>>): Promise<T> {
  const { data, error, response } = await call
  if (!response.ok) {
    const body = (error ?? {}) as Partial<ErrorBody>
    const retry = Number(response.headers.get('Retry-After'))
    throw new ApiError(
      response.status,
      body.code ?? 'internal',
      body.message ?? response.statusText,
      Number.isFinite(retry) && retry > 0 ? retry : undefined,
    )
  }
  return data as T
}

const messages: Record<string, string> = {
  invalid_credentials: "That username and password don't match.",
  invite_invalid: 'This invite has expired or was already used. Ask for a new one.',
  reset_link_invalid: 'This reset link has expired or was already used. Ask an admin for a new one.',
  username_taken: 'That username is taken. Try another.',
  passkey_failed: "Your passkey couldn't be verified. Try again.",
  ceremony_expired: 'That took too long. Try again.',
  last_credential: "You can't remove your only way to sign in.",
  wrong_password: "That isn't your current password.",
  account_disabled: 'An admin has disabled your account. Ask them to turn it back on.',
  last_admin: 'Someone else has to be an admin first: there always has to be one who can sign in.',
  service_rejected_credentials: "The service didn't accept those details.",
  service_unavailable: "Couldn't reach the service. Check the address and try again.",
  service_rate_limited: 'The service is busy. Try again in a minute.',
  different_account: "That's a different account. Link it separately, or unlink this one first.",
  needs_relink: 'This service needs you to link it again.',
  forbidden: "You don't have access to that.",
  no_player: 'No speaker yet. Open Syncphony on the phone connected to the speaker.',
  not_player: 'Another device took over as the speaker.',
  nothing_playing: 'Nothing is playing.',
  not_streamable: "That song can't play right now.",
  not_queued: "That song isn't waiting in the queue anymore.",
  unauthenticated: 'Sign in to continue.',
  cross_origin: 'Blocked a request from another site.',
  // OAuth callback outcomes (?link_error=)
  denied: 'Linking was cancelled.',
  bad_request: "The service sent back something unexpected. Try again.",
  oauth_state: 'That link attempt expired. Try again.',
  pairing_expired: 'That code expired. Try again.',
  not_paired: 'Approve the code first.',
  pairing_invalid: "No screen is showing that code. Check it and try again.",
  too_many_pairings: 'Too many screens are waiting to pair. Try again in a few minutes.',
  guest_pass_invalid: 'That guest code has expired. Ask someone in the room to show it again.',
  pass_full: 'This guest code is full. Ask for a new one.',
  guests_off: "This room doesn't let guests join. Its owner can turn that on in room settings.",
  not_tonight: 'Hearts are for songs playing tonight.',
  nothing_played: 'Nothing has played since the last night ended.',
}

/** A sentence to show the user for an error from the API or the browser. */
export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === 'rate_limited') {
      return err.retryAfter
        ? `Too many attempts. Try again in ${formatWait(err.retryAfter)}.`
        : 'Too many attempts. Try again in a bit.'
    }
    // not_playable's and guest_limit's messages name the song and service, or the limit.
    if (['invalid_input', 'bad_request', 'not_playable', 'guest_limit'].includes(err.code)) return capitalize(err.message)
    return messages[err.code] ?? 'Something went wrong. Try again.'
  }
  if (typeof err === 'string') return messages[err] ?? 'Something went wrong. Try again.'
  if (err instanceof TypeError) return "Couldn't reach Syncphony. Check your connection."
  return 'Something went wrong. Try again.'
}

function formatWait(seconds: number) {
  if (seconds < 60) return `${seconds} second${seconds === 1 ? '' : 's'}`
  const m = Math.ceil(seconds / 60)
  return `${m} minute${m === 1 ? '' : 's'}`
}

function capitalize(s: string) {
  return s ? s[0].toUpperCase() + s.slice(1) : s
}
