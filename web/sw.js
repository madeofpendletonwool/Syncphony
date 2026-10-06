// Syncphony's service worker: the app shell, offline. See docs/adr/0007-pwa-and-native-audio.md.
//
// This is a template. The build (vite.config.ts) fills in the version and
// the list of files to precache, and writes it to dist/sw.js.
//
// - Navigations: network first, so a deploy shows up at once; offline,
//   the cached shell.
// - /assets/*: fingerprinted and immutable, so cache first.
// - Everything else (the API, the room socket, audio streams, artwork):
//   never touched. Music needs the server; stale data would only mislead.

const VERSION = '__VERSION__'
const PRECACHE = __PRECACHE__
const CACHE = `syncphony-${VERSION}`
// '/', not '/index.html': the server redirects that to '/', and a
// redirect can't stand in for a page.
const SHELL = '/'

self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(CACHE).then((c) => c.addAll([SHELL, ...PRECACHE])))
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k.startsWith('syncphony-') && k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  )
})

// The page asks a waiting worker to take over when the user taps "Reload".
self.addEventListener('message', (event) => {
  if (event.data === 'skip-waiting') self.skipWaiting()
})

self.addEventListener('fetch', (event) => {
  const req = event.request
  if (req.method !== 'GET') return
  const url = new URL(req.url)
  if (url.origin !== self.location.origin) return

  if (req.mode === 'navigate') {
    // The API and socket are never navigated to, but be sure.
    if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/ws/')) return
    event.respondWith(
      fetch(req).catch(() => caches.match(SHELL).then((r) => r || Response.error())),
    )
    return
  }

  // Icons and the manifest: fresh when online, cached when not.
  if (PRECACHE.includes(url.pathname) && !url.pathname.startsWith('/assets/')) {
    event.respondWith(fetch(req).catch(() => caches.match(req).then((r) => r || Response.error())))
    return
  }

  if (url.pathname.startsWith('/assets/')) {
    event.respondWith(
      caches.match(req).then(
        (hit) =>
          hit ||
          fetch(req).then((res) => {
            if (res.ok) {
              const copy = res.clone()
              void caches.open(CACHE).then((c) => c.put(req, copy))
            }
            return res
          }),
      ),
    )
  }
})
