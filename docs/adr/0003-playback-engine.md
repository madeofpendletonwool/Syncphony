# ADR 0003: Playback engine

- **Status:** accepted
- **Date:** 2026-10-05
- **Issue:** MAD-691

## Context

A room's queue (ADR 0002) says what plays next. Something has to actually play it: one phone connected to a Bluetooth speaker, while everyone else's phone is a remote that shows what's on and can pause or skip. Songs come from different services that play in different ways. Navidrome hands us audio bytes. Spotify Connect plays on its own device and only takes instructions.

## Decision

### The server is the authority

`internal/playback` keeps each room's playback state in memory: the current song, its state, the position at a server timestamp, and the speaker device. Clients never decide what's playing. They send commands and the speaker sends reports. Everyone receives the same `nowplaying.updated` state and extrapolates the position from `positionMs` and `at`.

Every change to which song is current goes through `queue.Service.Change`, so it's serialized with queue edits, bumps the queue version, and writes `play_history`. The fair order then sees who just played.

### State machine

```
idle ──► loading ──► playing ◄──► paused
  ▲                     │
  └──── (queue empty) ◄─┴─► ended ──► loading (next song)
```

`ended` is momentary. The engine ends the song and starts the next one in a single step, so `ended` is never published.

A room only plays while a device has **claimed** it as the speaker (`PUT /rooms/{id}/player`). Claiming an idle room starts the first waiting song, and songs added to an idle room with a speaker start right away. Another device can take over at any time; it gets a new `revision` and reloads the song at the current position. Releasing the speaker pauses the room.

`revision` increases on every change the speaker must act on (a new song, play, pause, seek). A speaker applies each revision once, so commands can't be lost or applied twice.

### Two drivers, one state machine

The song's service decides the driver, by whether its session implements `provider.Streamer` or `provider.Remote`:

- **Stream:** the speaker loads `/api/rooms/{id}/stream/{itemId}`. That endpoint proxies the service through the link of whoever queued the song, with HTTP Range support. If the player can't decode the format, it is transcoded with ffmpeg (`internal/transcode`, driven by `?accept=`). The speaker reports `playing`, `progress`, `paused`, `ended` and `error`. The `next` song is included in the state so the speaker can preload it.
- **Remote:** the engine calls `Play`, `Pause`, `Resume` and `Seek`, and polls `State` every 2s. It detects the end of a song when the player stops at the end or moves on to another track. Changes made in the service's own app (a pause, say) are reflected back into the room.

### Never stall the party

| Problem | Response |
| --- | --- |
| The song can't start (link removed, service error, speaker reports `error`) | Skip it and push a `playback.notice` |
| A streamed song doesn't start within 20s | Skip it with a notice |
| The speaker never reports `ended` | Move on 15s after the song's length |
| The speaker goes quiet for 2 minutes while playing | Pause with a notice. The timeout is generous because mobile browsers throttle background tabs |
| 3 songs in a row fail | Stop, with a notice, instead of skipping through the whole queue. Pressing play resumes |

### Permissions

A room's owner sets who may do each thing (MAD-701): `playPause`, `seek` and `speaker` are `everyone` or `owner`, and `skip` is `everyone`, `owner` or `vote`. The owner can always do everything, and anyone can skip their own song. Rooms created before this had a single `controls` setting; it's read as the default for each permission and never written again, so no migration is needed. A skip can carry the `itemId` it targets, so two people tapping skip at the same time skip one song.

#### Vote to skip

When `skip` is `vote`, the owner and the song's requester can still skip outright, and everyone else votes (`vote_skip`, and `unvote_skip` to take it back). Votes live in memory with the rest of the room's state and reset when the song changes. `nowplaying.updated` carries the tally: who voted and how many votes are `needed`.

The vote passes once more than `skipVotePercent` (default 50, a majority) of the room has voted. "The room" is everyone connected to it, plus anyone who voted and has since left, minus whoever queued the song, since they'd just skip it. The engine re-counts whenever someone joins or leaves and whenever the owner changes the settings, so a vote can pass because a member walked away. A passed vote skips with a `playback.notice`.

### Restarts

Playback state lives in memory. After a restart, a song that was playing comes back **paused at its start**: nobody knows how far the old speaker got. The stored player device is cleared, and the speaker claims the room again when it reconnects.

## Consequences

- Each room's state sits behind one mutex, held during provider calls, so commands for one room never interleave. A slow service only delays its own room.
- Like the queue lock, this assumes a single server process. Running several would need the state and the lock moved into shared storage.
- Clients get every position from the server and need no clock sync, beyond knowing that `at` is the server's time.
