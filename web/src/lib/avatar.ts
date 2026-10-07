import {
  Bird, Bot, Cat, Cherry, Coffee, Crown, Disc3, Dog, Drum, Fish, Flame, Flower2, Gamepad2, Ghost, Guitar,
  Headphones, Heart, MicVocal, Moon, Mountain, Music, Piano, Pizza, Rabbit, Radio, Rocket, Skull, Snowflake,
  Sparkles, Speaker, Squirrel, Star, Sun, Turtle, Zap, Leaf, type LucideIcon,
} from 'lucide-react'

/** Icons anyone can pick as their avatar, saved as `icon:<name>`. */
export const AVATAR_ICONS: Record<string, LucideIcon> = {
  headphones: Headphones, music: Music, guitar: Guitar, 'mic-vocal': MicVocal, 'disc-3': Disc3, radio: Radio,
  drum: Drum, piano: Piano, speaker: Speaker, cat: Cat, dog: Dog, bird: Bird,
  rabbit: Rabbit, fish: Fish, turtle: Turtle, squirrel: Squirrel, rocket: Rocket, star: Star,
  flame: Flame, zap: Zap, ghost: Ghost, skull: Skull, heart: Heart, moon: Moon,
  sun: Sun, leaf: Leaf, flower: Flower2, cherry: Cherry, pizza: Pizza, coffee: Coffee,
  gamepad: Gamepad2, crown: Crown, sparkles: Sparkles, snowflake: Snowflake, mountain: Mountain, bot: Bot,
}

export type AvatarKind = 'initials' | 'icon' | 'photo' | 'gravatar' | 'link'

const GRAVATAR_HOSTS = new Set(['gravatar.com', 'www.gravatar.com', 'secure.gravatar.com'])

/** What kind of avatar a user's `avatar` field holds. */
export function avatarKind(avatar: string | undefined): AvatarKind {
  if (!avatar) return 'initials'
  if (avatar.startsWith('icon:')) return 'icon'
  if (avatar.startsWith('/api/users/')) return 'photo'
  try {
    const u = new URL(avatar)
    if (GRAVATAR_HOSTS.has(u.hostname) && u.pathname.startsWith('/avatar/')) return 'gravatar'
  } catch {
    // Not a full URL; treat it as a link.
  }
  return 'link'
}

/** The icon an `icon:<name>` avatar names, if it's one we know. */
export function avatarIcon(avatar: string | undefined): LucideIcon | undefined {
  return avatar?.startsWith('icon:') ? AVATAR_ICONS[avatar.slice(5)] : undefined
}

/**
 * The Gravatar for an email. `d=404` makes a missing one fail to load, so the
 * avatar falls back to initials instead of Gravatar's placeholder.
 */
export function gravatarUrl(email: string) {
  return `https://gravatar.com/avatar/${sha256(email.trim().toLowerCase())}?s=256&d=404`
}

const K = Uint32Array.from([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5, 0xd807aa98,
  0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
  0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da, 0x983e5152, 0xa831c66d, 0xb00327c8,
  0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
  0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819,
  0xd6990624, 0xf40e3585, 0x106aa070, 0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
  0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7,
  0xc67178f2,
])

/**
 * Hex SHA-256 of a string. Synchronous and dependency-free because
 * crypto.subtle is missing on plain-HTTP servers, which self-hosters run.
 */
export function sha256(text: string) {
  const data = new TextEncoder().encode(text)
  const len = Math.ceil((data.length + 9) / 64) * 64
  const msg = new Uint8Array(len)
  msg.set(data)
  msg[data.length] = 0x80
  const view = new DataView(msg.buffer)
  view.setUint32(len - 8, Math.floor((data.length * 8) / 2 ** 32))
  view.setUint32(len - 4, (data.length * 8) >>> 0)

  const h = Uint32Array.from([
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
  ])
  const w = new Uint32Array(64)
  const rotr = (x: number, n: number) => (x >>> n) | (x << (32 - n))
  for (let off = 0; off < len; off += 64) {
    for (let i = 0; i < 16; i++) w[i] = view.getUint32(off + i * 4)
    for (let i = 16; i < 64; i++) {
      const s0 = rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >>> 3)
      const s1 = rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >>> 10)
      w[i] = w[i - 16] + s0 + w[i - 7] + s1
    }
    let [a, b, c, d, e, f, g, hh] = h
    for (let i = 0; i < 64; i++) {
      const t1 = hh + (rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25)) + ((e & f) ^ (~e & g)) + K[i] + w[i]
      const t2 = (rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22)) + ((a & b) ^ (a & c) ^ (b & c))
      hh = g
      g = f
      f = e
      e = (d + t1) >>> 0
      d = c
      c = b
      b = a
      a = (t1 + t2) >>> 0
    }
    h[0] += a
    h[1] += b
    h[2] += c
    h[3] += d
    h[4] += e
    h[5] += f
    h[6] += g
    h[7] += hh
  }
  return Array.from(h, (x) => x.toString(16).padStart(8, '0')).join('')
}
