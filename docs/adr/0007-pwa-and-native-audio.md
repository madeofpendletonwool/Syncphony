# ADR 0007: Installable web app first, native shell when iOS says so

- **Status:** accepted
- **Date:** 2026-10-05
- **Issue:** MAD-705

## Context

Syncphony is used at hangouts from phones, and one phone plays the music (player mode, ADR 0003). People should be able to install it, and it should open even when the server can't be reached for a moment.

The open question from Phase 2 is iOS. Will a locked iPhone keep playing the queue from a web page? Or do we need a native shell (Capacitor, MAD-707) with a real audio session? The Phase 2 player was verified in desktop Chromium, but the iOS rows of the test matrix in `docs/player-mode.md` haven't been run on a device yet.

## Decision

### An installable web app

- **The manifest** has a stable `id`, standalone display, maskable icons, and shortcuts to Room, Search and History.
- **On Chromium (Android, desktop)**, the Me page has an **Install** button, using the browser's install prompt. On iOS, which has no prompt, it shows the two steps (Share, then Add to Home Screen). Once installed, the card goes away.

### A service worker for the shell

`web/sw.js` is a template. The build (`vite.config.ts`) fills in:
- the list of files to precache: every fingerprinted asset, the icons, and the manifest (about 1 MB);
- a version that changes whenever any of those change, or the worker itself does.

What it does:
- **Navigations** go to the network first, so a deploy shows up at once. Offline, they get the cached shell. The shell is cached as `/`, not `/index.html`, which the server redirects, and a redirect can't stand in for a page.
- **`/assets/*`** is served from the cache first, since those files are immutable.
- **The API, the room socket, audio streams and artwork** are never touched. Music needs the server, and stale data would mislead.
- **The server** sends `sw.js`, `index.html` and the manifest with `no-cache`, so browsers notice new builds.
- **Updates:** when a new worker is waiting, the app offers "A new version is ready · Reload". It doesn't take over a running speaker unasked. A long-lived tab checks for updates hourly.
- **Opening offline:** the app remembers the signed-in profile on the device, so it opens offline as you, with an offline banner. That's only for display: the server checks the session on every request, and a 401 or signing out forgets it. Anything that still can't load shows a "Can't reach Syncphony" screen with a retry, instead of a blank page.

This was verified in headless Chromium against a production build:
- the worker installs, takes control, and precaches the shell;
- signed in and offline, `/room`, `/me` and `/search` all render;
- the manifest is served from the cache;
- there are no page errors.

### iOS background audio: mitigate now, decide on evidence

The biggest known risk, from `docs/player-mode.md`, is that iOS may refuse to start the *other* `<audio>` element from the `ended` handler while the phone is locked. The queue would then stop after one song.

The speaker now handles that. If starting the preloaded element is refused (`NotAllowedError`), it plays the next song in the element that just ended, which iOS allows. It remembers this, and handles later background handoffs in place straight away. Everywhere else, nothing changes: the gapless two-element handoff is kept.

**We're staying a web app for now**, and not pulling Capacitor (MAD-707) forward. Instead, we'll run the device checklist in `docs/player-mode.md` on an iPhone (Safari tab and Home Screen app). Capacitor moves up if any of these happen:

1. With the in-place fallback, the queue still stops after a song while the phone is locked (checklist step 3).
2. The room pauses itself while locked because reports stop arriving for 2 minutes, which is the server's timeout.
3. Remote pause, skip and seek reach a locked speaker so late (the socket is suspended) that the room can't be controlled.
4. The lock screen and Bluetooth controls don't work from the Home Screen app.

Android is expected to be fine as a web app. It still needs one device run to confirm.

## Consequences

- People can install Syncphony today on Android, iOS and desktop, and it opens offline.
- The service worker is plain JavaScript filled in at build time, with no plugin dependency. It's small enough to read in one sitting.
- A wrong offline shell is a stale page, not data loss. If a worker ever misbehaves, deleting `sw.js` from the build and shipping an empty worker clears it.
- The Capacitor decision now has written triggers, so it's made from device results rather than guesswork.
