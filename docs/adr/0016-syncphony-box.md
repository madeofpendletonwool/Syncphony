# ADR 0016: The Syncphony box

- **Status:** accepted
- **Date:** 2026-10-09
- **Issue:** MAD-802 (parent: MAD-797, stage 2; the box's own repo is `madeofpendletonwool/syncphony-box`)

## Context

A Raspberry Pi flashed with the Syncphony Box image plugs into a TV and becomes the room's big screen *and* its speaker (MAD-797). It runs no Syncphony server: it's a kiosk for one. On the box, cage runs Chromium fullscreen at `<server>/tv?box=<boxVersion>&name=<name>` with `--autoplay-policy=no-user-gesture-required`, where `name` comes from `syncphony.txt` on the boot partition.

The box also runs `boxd`, a small Go daemon that owns everything that isn't Chromium: CEC, the watchdog, config, and local screens. The page and `boxd` need to talk — the page knows when music starts; `boxd` knows whether the TV is off and how to restart Chromium. This ADR is the contract both sides follow; it's the source of truth for the bridge, and `boxd` implements it exactly.

## Decision

### Kiosk mode

When the `box` query parameter is present, `/tv` runs in kiosk mode. All of the box-specific web code lives in `web/src/lib/box.ts`; the rest of `/tv` only checks `isBox()`.

- **The speaker starts without a press.** A box pairs with audio on, and once paired it claims the speaker by itself the first time (and after every reload), as long as nobody else is the room's player — the same rule as the existing resume-after-reload path, minus the localStorage flag. It never takes over from another device: if someone's phone is playing, the box shows the same "Play here instead" button anyone would. The box's Chromium allows autoplay without a gesture, so this works; on any browser that still blocks it, `play()` rejects and the "Press OK to start the audio" button returns as the fallback.
- **It pairs as a box.** `POST /api/display/pairing` takes what the screen says about itself: `kind: "box"` and a `suggestedName` (from `?name=`). Whoever types the code in sees the right defaults — the audio toggle on, the suggested name — but their choices are final: the name they type wins, and audio is on only if they say so (or leave the toggle on). While someone types a code, `GET /api/rooms/{roomId}/displays/pairing/{code}` (`lookupDisplayPairing`) tells their phone what's behind it, so the defaults are right before the button is pressed.
- **No prompts for a person at a laptop.** Nothing on a box asks anyone to install the PWA, hints at keyboard shortcuts, or says "open in app". A new version of the web app, which would normally offer a reload toast, just reloads: nobody is standing there to tap it.

### The box bridge

The page talks to `boxd` at **`http://127.0.0.1:8099`**, paths under `/v1`, JSON bodies. Loopback counts as a secure origin, so an HTTPS page may call it without a mixed-content error. Every call is optional and fails silently: if `/v1/info` doesn't answer, the page behaves like a normal `/tv` — pairing, playback and the speaker never depend on `boxd`.

- **`GET /v1/info`** → `{version, name, capabilities: ["cec", "reboot", ...]}`. What the box is and what it can do; the page probes it when it starts (and keeps trying alongside heartbeats until it answers).
- **`POST /v1/events`** ← `{type: "playing" | "paused" | "idle" | "paired" | "unpaired", roomId?}`. Sent when the room's playback changes (playing, paused, or nothing queued) and when the display is paired or unpaired. The box uses `playing` for CEC — switch the TV on and to this input when music starts — and all of them for health. Consecutive repeats aren't sent.
- **`POST /v1/heartbeat`** every 30 s while the page is healthy. If heartbeats stop while the server's `/healthz` answers, `boxd`'s watchdog restarts Chromium (that policy is the box repo's, stage 3).
- **`POST /v1/reboot`** and **`POST /v1/reload`** reboot the box or reload its page. They're for remote care from the room's Screens list (stage 4): the server tells the page over its room socket, and the page relays. Nothing calls them before that.

`boxd` listens on loopback only, checks every request's `Origin` against the configured server's origin, and answers CORS preflights — including the Private Network Access preflight Chromium sends before an HTTPS page posts to loopback — with `Access-Control-Allow-Origin: <that origin>` and `Access-Control-Allow-Private-Network: true`. Requests from any other origin are refused. The bridge is versioned (`/v1`) so the page (which updates with the server) and the box (which updates with the image) can drift briefly without breaking each other.

## Consequences

- Pairing gains a body: `POST /display/pairing` accepts `kind` and `suggestedName`, and the room-scoped lookup tells the pairing phone what it's about to pair. Nothing is stored: both live on the pending pairing in memory, and the display itself is unchanged.
- The page never waits on `boxd`: a missing, slow, or wrong-version daemon can only cost CEC and watchdog care, never music.
- `/tv` outside kiosk mode is untouched: no bridge calls leave the page, and the speaker still needs its one press.
