# ADR 0006: Cross-service matching

- **Status:** accepted
- **Date:** 2026-10-05
- **Issue:** MAD-704

## Context

A song plays through the link of whoever queued it. If that link expires, is unlinked, or its service refuses the track, the song is skipped (ADR 0003). Often someone else in the room has the same recording on their own service. Separately, a song that's only on someone's private Spotify can't be queued by anyone else, because they can't search it.

## Decision

### Matching (`internal/match`)

Matching is strict. Skipping beats playing the live version, a remix, or a different song with the same name.

- **The same ISRC** is a match, scored 1. Navidrome (OpenSubsonic) reports ISRCs. Spotify's Web API stopped returning them, so Spotify matches go by the next rule.
- **Otherwise, a careful fuzzy match.** It's never scored 1, so a link with an ISRC match always wins.
  - Titles must agree once normalized: case, accents and punctuation are ignored, and so are harmless qualifiers like "2011 Remaster" and "feat. X".
  - Both must be the same kind of version: live, remix, acoustic, edit, and so on.
  - An artist must overlap, with "A & B" counting as both and "The" ignored.
  - Lengths must be within 10 seconds when both are known.
- **The search** runs on each candidate link: title and first artist, then title alone, since services search differently. It keeps the best score across links. A service that can say up front it won't play a track (`PlayChecker`) is asked.

### Where it looks

The candidates are:
- links of the people **connected to the room right now**,
- links of whoever queued the song,
- **shared** links,

leaving out expired links and the ones the song already used. Someone who isn't here isn't drawn on. Their account isn't playing for a party they aren't at.

### Falling back

`playback.Engine.begin` tries the song's own source first. If it can't start, the engine looks for a stand-in. The same happens when the speaker reports an error on a streamed song, before the song is skipped.

A stand-in is recorded on the queue item (`via_provider`, `via_link_id`, `via_track_id`, migration 4) through `queue.Change`. That way the stream endpoint, remote polling, a restart, and the queue snapshot all agree on where the song comes from. The room gets a notice ("Playing “X” from Sam's Navidrome: Spotify couldn't play it"), and the UI tags the song with the service it's playing from.

Each song gets one stand-in. If that fails too, the song is skipped as before. The search holds the room's lock (ADR 0003), so it's capped at 15 seconds, under the 20-second load timeout. Each link gets 6 seconds.

`matching.fallback` turns this off for a room. It's on by default: the point is to keep the party going, and it only uses services of people who are there.

### Borrowing

`TrackToQueue.fromItemId` queues the song of one of the room's items again (from history or stats), through the same link and with the same metadata snapshot. If you can use that link, this always works. If it's someone else's private link, it needs the room's `matching.borrow`, which is off by default (`cant_borrow` otherwise). Borrowed songs fall back like any other.

## Consequences

- A fallback can spend up to 15 seconds searching before the song starts. That's acceptable next to skipping it.
- Spotify matches are fuzzy, so a wrong match is possible but unlikely given the version, artist and length checks.
- Using another member's subscription to play someone else's song is a judgment call. It's limited to people in the room, announced each time, and can be turned off.
