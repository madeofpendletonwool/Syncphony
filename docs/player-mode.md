# Player mode

One phone in the room is the **speaker**. It's usually connected to a Bluetooth speaker. It plays the queue, and everyone else's phone acts as a remote. The server decides what plays (see [ADR 0003](adr/0003-playback-engine.md)). The speaker applies each state change once and reports back what the audio actually did.

The code is in `web/src/lib/speaker.ts`, with its UI in `web/src/components/room/speaker-panel.tsx`.

## How it plays

- **Plain `<audio>` elements, not Web Audio.** Mobile browsers suspend an `AudioContext` in the background but keep a playing media element going, which also lets the OS treat it as a media session.
- **Two elements take turns.** The server's state includes the `next` song. The idle element loads it ahead of time. When the current song ends, the speaker starts the preloaded one at once, then reports `ended`. When the server's new state arrives, the speaker confirms with `playing`. In testing, the server caught up about 0.4s after the audio switched.
- **Unlocking:** browsers only allow audio after a tap. Tapping **Play on this phone** plays a tenth of a second of silence on both elements before anything async happens. That keeps them both allowed to play later. If the browser still blocks playback, the panel shows **Tap to start the audio**.
- **Format:** the stream URL carries `accept=` with the types this browser says it can play (`canPlayType`). The server transcodes anything else.
- **Reports:** `playing` when audio starts, `progress` every 5s while playing, `paused` when something outside the app pauses it (a Bluetooth disconnect, a phone call), `ended`, and `error` after two retries.
- **Network blips:** a failed stream is reloaded from the same position, with backoff. The room socket reconnects on its own and catches up. If the server forgets the speaker (after a restart), the speaker claims the room again. If another device takes over, this one stops and says so.
- **Media Session:** sets the lock-screen title, artist, album, and artwork, plus the position. Play, pause, next, previous (restart the song), and seek are wired to the room. So the speaker's and headphones' buttons control the room, subject to the room's permissions.
- **Screen Wake Lock:** **Keep the screen on** is on by default and remembered per device. The lock is requested again whenever the page becomes visible, because browsers drop it when the page is hidden.

## Test matrix

| Platform | Status | Notes |
| --- | --- | --- |
| Desktop Chromium (headless, automated) | ✅ Verified | Claim, real audio progress reports, pause/resume from another phone (position held), song end with gapless handoff, skip, takeover by another device, release, a 4s offline blip, lock-screen metadata. |
| Android Chrome, tab, screen locked | ⏳ Not yet tested on a device | Expected to work well: Chrome keeps media playing and fires its events in the background. |
| Android Chrome, installed PWA | ⏳ Not yet tested | Same as the tab, expected. |
| iOS Safari, tab, screen locked | ⏳ Not yet tested on a device | See the iOS notes below. |
| iOS Safari, Home Screen PWA, screen locked | ⏳ Not yet tested on a device | See the iOS notes below. This is the setup most likely to be used at a hangout. |

### Checklist for a device run

Do each with the speaker phone connected to a Bluetooth speaker, then repeat with the screen locked:

1. Tap **Play on this phone**. Audio starts, and the lock screen shows the song and its artwork.
2. Let a song end on its own. The next one starts with no audible gap.
3. Lock the screen for 10+ minutes across several songs. Playback continues and the room's position stays right on another phone.
4. From another phone: pause, play, skip, seek. The speaker follows within about a second.
5. Use the Bluetooth speaker's own play/pause and next buttons. The room follows.
6. Turn off Wi-Fi for 10s mid-song (stay on cellular, or turn it back on). Playback continues or resumes, and the room recovers.
7. Take a phone call or play a voice memo, then return. The room shows paused, and pressing play resumes.
8. With **Keep the screen on** enabled and unlocked, the screen doesn't dim.

Record what happens in the matrix above, especially anything under the iOS notes.

## iOS: what to expect and what to verify

These come from how Safari is documented and known to behave, **not** from device testing yet. Each is something to confirm or rule out on a real iPhone. Together they feed the Capacitor decision in Phases 4–5.

- **Starting a song in the background is the biggest risk.** A playing media element keeps going when the screen locks. But the gapless handoff starts the *other* `<audio>` element from the `ended` handler. iOS may refuse to start a different element while the page is in the background. If that happens, the symptom is that the queue stops after the first song once the phone is locked. **Handled (MAD-705):** if the browser refuses, the speaker plays the next song in the element that just ended, by swapping its `src`. That's slightly less gapless, but generally allowed in the background. It then keeps doing that for songs that start while hidden. When testing, check that step 3 plays several songs in a row while locked.
- **JavaScript is throttled while locked.** Progress reports may arrive late or not at all while the screen is off. The server pauses a room only after 2 minutes without reports, and media events (`ended`, `timeupdate`) usually still fire while audio plays. Watch for the room pausing itself during step 3.
- **The WebSocket may be suspended in the background.** The speaker doesn't need the socket to keep playing songs it already knows about, but it won't hear about remote pauses or skips until the page wakes. Watch for laggy remote control during step 4 with the screen locked.
- **Screen Wake Lock** needs a recent iOS. Installed Home Screen apps have had bugs with it in some versions. Check step 8 both in a Safari tab and as a Home Screen app.
- **Autoplay:** a Home Screen app that iOS has evicted from memory starts fresh and needs another tap. The **Tap to start the audio** button covers this, but someone has to be there to press it.
- **Interruptions:** after a call, iOS pauses the element. The speaker reports `paused`, and someone has to press play. iOS doesn't auto-resume web audio.

If background handoff (the first item) or the reports/socket behavior (the next two) turn out to be unreliable on iOS, that argues for doing the native shell (Capacitor, with a native audio session) sooner rather than later. [ADR 0007](adr/0007-pwa-and-native-audio.md) lists the exact triggers.

## Installing

On Android and desktop Chrome, use **Install** on the Me page. On iOS, tap Share in Safari, then **Add to Home Screen**. The installed app opens offline (see ADR 0007), but playing music always needs the server.
