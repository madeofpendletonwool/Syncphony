# ADR 0010: Suggestions to keep the vibe going

- **Status:** accepted
- **Date:** 2026-10-06
- **Issue:** MAD-740

## Context

When someone opens Search to add a song, they often don't have one in mind. The room already says a lot about what to play next: what played through, what's waiting, what's on now. Autopilot (ADR 0008) turns that into songs when the queue runs dry, but only in the background, one at a time, and as nobody's pick. We want the same idea in people's hands: a list of songs to add with a tap, split into **your vibe** (like the songs you picked) and **group vibe** (like everyone's).

## Decision

### One way of finding songs, shared with autopilot

The parts of autopilot that find songs like a seed move into `internal/suggest`: song keys (service and ID, ISRC, artist and title), the `Seen` set, resolving a seed on another link (`match.On`, then the artist's name), asking a `Recommender` for similar songs (`Finder.Similar`), and the artist's-catalog fallback. Autopilot keeps what's its own: when to fill, seed turns across its fills, skip memory, picking one song. Its behavior didn't change.

### `GET /rooms/{roomId}/suggestions?scope=mine|group&source=history|queue`

- **Seeds** are the song playing, then the songs waiting in fair order, then the songs that played through, newest first. `source=queue` (MAD-749) stops after what's playing and waiting, so a queued change of vibe is reflected; what played through stands in when nothing's queued. Skipped songs and autopilot's don't seed (autopilot's `added_by` is whose taste seeded it, not who chose it). `mine` keeps only the asker's; `group` takes turns between members, so one person's run of songs doesn't take over. A list is built from at most six seeds.
- **Songs** come from the asker's usable links only (their own and shared ones), so every suggestion can be queued by them. Services that recommend give similar songs and the artist's top songs. Services that only search (Spotify, nugs.net) give more by the seed's artist, found by searching their name. Each seed asks at most two services, recommenders first.
- **Not suggested:** anything waiting, playing, or in the room's last 200 plays — nor songs by an artist the room skipped a song of without ever finishing or queuing one of theirs: a skip turns that vibe away, a full listen or a queued song brings it back. Songs are compared the way autopilot compares them.
- **The list** mixes seeds, taking turns. Picks within a seed are random among the top of its candidates, so a reshuffle gives a different list. Each suggestion carries its seed (`because`), for a byline like "Like Heroes, Bob's pick".
- **Cost:** a list is cached in memory for two minutes per room, person, scope, source and queue version; `refresh=true` builds a new one. Building one is bounded at 15 seconds; whatever was found by then is returned.
- Guests can ask in their own room; they get songs from the server's shared services.

### On the Search screen

Before you type, Search shows **Keep the vibe going** above the playlist shelves, with a Your vibe / Group vibe toggle, a History / Queue toggle beside it, and a shuffle button. The list is fetched again when the song playing changes, not on every queue change, so it stays put while you add from it; songs you add show as added.

## Consequences

- Suggestions are only as good as the services. Navidrome with Last.fm agents gives similar songs; without them, and on Spotify, it's more by the same artists. Spotify's recommendations API is closed to new apps (ADR 0008), so this won't get better there through that API.
- A room nobody has played or queued in yet gets an empty list, with a hint to queue a few songs.
- Searching a search-only service by artist name can return songs where that artist isn't first; those are dropped, so a common name may give fewer suggestions.
- The cache is in-process, like the queue's lock (ADR 0002).
