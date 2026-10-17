# ADR 0019: Room devices

- **Status:** accepted
- **Date:** 2026-10-17

## Context

Only the device itself can become a room's speaker: `PUT /rooms/{id}/player` takes the caller's own device (ADR 0003). That's fine for a phone in your hand, but not for a screen across the room. Once a Syncphony box (ADR 0016) stops being the speaker — someone takes over, or presses Stop — the only way back is to bring the TV up and press "Play the audio here" with the remote.

A room should show what's connected to it, and let people hand the speaker around from wherever they are.

## Decision

### The room lists its devices

`GET /rooms/{id}/devices` lists the room's paired screens (online or not), then every signed-in browser with the room open right now. Paired screens' online state already comes from ADR 0016. Browsers now say what they are on the room socket: `device` (as before), `kind` (`computer` or `phone`), and `name` (`Mac`, `iPhone`). A browser that doesn't say is a phone, so it's never sent the speaker by mistake. A big screen opened while signed in (`display=1`) is a screen. Browser tabs share a device ID, so they're one device.

All of this is in memory, like the box registry (no migration). The room hears `devices.updated` when a browser first opens it or last closes it; screens still say `displays.updated`.

Each device carries `canSpeak` and `canStop` for whoever's asking, so clients don't re-derive the rules.

### The device claims the room itself

`PUT /rooms/{id}/devices/{deviceId}/speaker` doesn't make the device the speaker. It sends `device.command {action: "speak"}` to that device alone, and the device claims the room through the usual `PUT /player`, under its own credentials and its own name. The device is the one that has to start the audio, so it's the one that should claim. A screen's command goes on its display topic. A browser's goes on its user's topic, and the server only passes it to the socket whose device ID and room match.

On a box, Chromium allows autoplay, so the music starts by itself. On any other screen or computer that still blocks it, the existing "Press OK / Tap to start the audio" button appears.

The `/tv` page acts on it the same way as pressing its own button: it starts the room if it was paused and nobody else had it, and it remembers to pick up again after a reload. In the app, the room's live component does the same as "Play on this device".

### Who may send it where

- **A paired screen:** whoever may manage it (the room's owner, whoever paired it, or an admin), with its audio on, while it's online. It plays as whoever paired it, so both they and the caller need the room's `speaker` permission.
- **A browser:** only your own, only a computer or a signed-in big screen, and you need the `speaker` permission. You can't push audio onto someone else's device. Phones are never sent the speaker (`device_is_phone`): their browsers only play after a tap on the phone itself.

### Stopping

Releasing a speaker from somewhere else used to be undone at once: a speaker that finds the server forgot it claims the room again (it assumes a server restart). So `DELETE /rooms/{id}/devices/{deviceId}/speaker` first sends `device.command {action: "stop"}` to the device, then lets go of it (`Playback.Drop`). The command reaches the device before the `nowplaying.updated` without a speaker, so the device has already gone quiet and doesn't reclaim. A screen can be stopped by whoever may manage it. A browser can be stopped by its user, the room's owner, or an admin. A speaker that isn't connected (a locked phone) can still be stopped: the release alone is enough there.

## Consequences

- The Devices list is in the room menu and under the speaker panel. The Screens list in the Big screen dialog gets "Make it the speaker" and "Stop the speaker" for screens with audio.
- From the Devices list, your own browser gets "Play here", which runs from the tap, so a phone can still make itself the speaker there.
- A box that dropped off as the speaker can be brought back from any phone in the room.
- Old clients that don't know `device.command` ignore it. Their sockets don't send `kind`, so they're listed as phones and never sent the speaker.
