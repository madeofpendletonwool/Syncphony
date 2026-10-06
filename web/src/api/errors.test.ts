import { describe, expect, it } from 'vitest'
import { ApiError, errorMessage, unwrap } from './errors'

const response = (status: number, headers: Record<string, string> = {}) => new Response(null, { status, headers })

describe('unwrap', () => {
  it('returns data on success', async () => {
    await expect(unwrap(Promise.resolve({ data: { ok: 1 }, response: response(200) }))).resolves.toEqual({ ok: 1 })
  })

  it('throws ApiError with code and Retry-After', async () => {
    const call = unwrap(
      Promise.resolve({
        error: { code: 'rate_limited', message: 'slow down' },
        response: response(429, { 'Retry-After': '30' }),
      }),
    )
    await expect(call).rejects.toMatchObject({ status: 429, code: 'rate_limited', retryAfter: 30 })
  })
})

describe('errorMessage', () => {
  it('maps known codes', () => {
    expect(errorMessage(new ApiError(409, 'username_taken', ''))).toMatch(/taken/)
  })
  it('includes the wait for rate limits', () => {
    expect(errorMessage(new ApiError(429, 'rate_limited', '', 90))).toBe('Too many attempts. Try again in 2 minutes.')
  })
  it('shows the server message for invalid input', () => {
    expect(errorMessage(new ApiError(400, 'invalid_input', 'username is too short'))).toBe('Username is too short')
  })
  it("shows the server message for a song the service won't play", () => {
    const msg = 'Spotify won\'t let Syncphony play "Mr. Brightside". Try another version, or add it from another service.'
    expect(errorMessage(new ApiError(422, 'not_playable', msg))).toBe(msg)
  })
  it('maps pairing codes', () => {
    expect(errorMessage(new ApiError(410, 'pairing_expired', ''))).toBe('That code expired. Try again.')
  })
  it('maps OAuth callback codes given as strings', () => {
    expect(errorMessage('denied')).toBe('Linking was cancelled.')
  })
  it('falls back for unknown errors', () => {
    expect(errorMessage(new Error('boom'))).toBe('Something went wrong. Try again.')
  })
})
