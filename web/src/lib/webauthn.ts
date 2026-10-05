// Passkey ceremonies. The server sends WebAuthn options as JSON
// (`{ publicKey: {...} }`) and takes back `PublicKeyCredential.toJSON()`.

type Options = { [key: string]: unknown }

/** Whether this browser can create and use passkeys with JSON options. */
export function passkeysSupported() {
  return (
    typeof window !== 'undefined' &&
    typeof window.PublicKeyCredential !== 'undefined' &&
    typeof PublicKeyCredential.parseCreationOptionsFromJSON === 'function' &&
    typeof PublicKeyCredential.parseRequestOptionsFromJSON === 'function'
  )
}

/** Thrown when the user dismisses the browser's passkey prompt. */
export class PasskeyCancelled extends Error {
  constructor() {
    super('passkey prompt dismissed')
    this.name = 'PasskeyCancelled'
  }
}

export async function createPasskey(options: Options) {
  const publicKey = PublicKeyCredential.parseCreationOptionsFromJSON(
    options.publicKey as PublicKeyCredentialCreationOptionsJSON,
  )
  return finish(navigator.credentials.create({ publicKey }))
}

export async function getPasskey(options: Options) {
  const publicKey = PublicKeyCredential.parseRequestOptionsFromJSON(
    options.publicKey as PublicKeyCredentialRequestOptionsJSON,
  )
  return finish(navigator.credentials.get({ publicKey }))
}

async function finish(ceremony: Promise<Credential | null>) {
  let cred: Credential | null
  try {
    cred = await ceremony
  } catch (err) {
    // NotAllowedError covers both "cancelled" and "timed out"; neither is
    // worth an error message.
    if (err instanceof DOMException && (err.name === 'NotAllowedError' || err.name === 'AbortError')) {
      throw new PasskeyCancelled()
    }
    throw err
  }
  if (!(cred instanceof PublicKeyCredential)) throw new PasskeyCancelled()
  return cred.toJSON() as unknown as Options
}

/** A default label for a new passkey, like "iPhone" or "Mac". */
export function deviceName(ua = navigator.userAgent) {
  if (/iPhone/.test(ua)) return 'iPhone'
  if (/iPad/.test(ua)) return 'iPad'
  if (/Android/.test(ua)) return 'Android'
  if (/Macintosh/.test(ua)) return 'Mac'
  if (/Windows/.test(ua)) return 'Windows'
  if (/CrOS/.test(ua)) return 'Chromebook'
  if (/Linux/.test(ua)) return 'Linux'
  return 'Passkey'
}
