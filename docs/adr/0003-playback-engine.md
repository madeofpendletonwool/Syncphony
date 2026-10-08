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
| …and the speaker said nothing about it the whole time (its tab is asleep, or gone) | Pause on it with a notice instead. The song isn't to blame, and skipping would burn the queue. The speaker acknowledges each song it starts loading (a `progress` report), so a speaker that's there but can't play the song still skips it. Play loads it again |
| The speaker never reports `ended` | Move on 15s after the song's length |
| The speaker goes quiet for 2 minutes while playing | Pause with a notice. The timeout is generous because mobile browsers throttle background tabs |
| 3 songs in a row fail | Stop, with a notice, instead of skipping through the whole queue. Pressing play resumes; the room shows a play button while it's stopped |

### Permissions

A room's owner sets who may do each thing (MAD-701): `playPause`, `seek` and `speaker` are `everyone` or `owner`, and `skip` is `everyone`, `owner` or `vote`. The owner can always do everything, and anyone can skip their own song. Rooms created before this had a single `controls` setting; it's read as the default for each permission and never written again, so no migration is needed. A skip can carry the `itemId` it targets, so two people tapping skip at the same time skip one song.

#### Vote to skip

When `skip` is `vote`, the owner and the song's requester can still skip outright, and everyone else votes (`vote_skip`, and `unvote_skip` to take it back). Votes live in memory with the rest of the room's state and reset when the song changes. `nowplaying.updated` carries the tally: who voted and how many votes are `needed`.

The vote passes once more than `skipVotePercent` (default 50, a majority) of the room has voted. "The room" is everyone connected to it, plus anyone who voted and has since left, minus whoever queued the song, since they'd just skip it. The engine re-counts whenever someone joins or leaves and whenever the owner changes the settings, so a vote can pass because a member walked away. A passed vote skips with a `playback.notice`.

#### Play now

`play_now` with a queued `itemId` makes that song current straight away, skipping the one playing. The fair order isn't changed: the song simply jumps it, and the fair order then sees its requester just played. It takes the `skip` permission. When `skip` is `vote`, the owner still plays it outright, and anyone else asks the room: `nowplaying.updated` carries the request (`playNow`), everyone gets a notice, and the room agrees with `vote_play_now` on the same share as a skip vote. One request is open at a time. It lapses after 2 minutes, when its song leaves the queue, or when whoever asked withdraws it (`unvote_play_now`).

#### Previous

`previous` plays the song before this one again: the latest finished play whose song isn't waiting or playing. The song that was playing goes back to the front of the queue (`resume_at`), ahead of the fair order, to play next from the start; its unfinished play is deleted, so it isn't counted as a skip and doesn't cost its owner a turn. Going back several times puts each song back, the latest first. Moving or removing a song gives up its place at the front. Like `play_now` it takes the `skip` permission, and in a room that votes the request is a `playNow` with `back` set and the song in `item`, since it isn't in the queue; it lapses when the playing song changes. Clients send it when back is pressed in a song's first 3 seconds, and restart the song otherwise.

#### One tap to play

With no speaker, a member who may be the speaker pressing play becomes the speaker and starts the room in the same tap. "Play on this phone" does the same for a paused room.

### Restarts

Playback state lives in memory. After a restart, a song that was playing comes back **paused at its start**: nobody knows how far the old speaker got. The stored player device is cleared, and the speaker claims the room again when it reconnects.

## Consequences

- Each room's state sits behind one mutex, held during provider calls, so commands for one room never interleave. A slow service only delays its own room.
- Like the queue lock, this assumes a single server process. Running several would need the state and the lock moved into shared storage.
- Clients get every position from the server and need no clock sync, beyond knowing that `at` is the server's time.
