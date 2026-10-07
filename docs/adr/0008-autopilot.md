# ADR 0008: Autopilot DJ

- **Status:** accepted; how songs are found, seeded and how adventurous they are is superseded by ADR 0012
- **Date:** 2026-10-06
- **Issue:** MAD-718

## Context

When everyone's lane runs dry, the room goes silent until someone picks up a phone. At a party that's the worst moment to stop. We want the room to keep going on its own with songs like the ones it's been playing, without breaking what makes Syncphony fair: songs belong to people, and people take turns.

## Decision

### Autopilot songs are nobody's

An autopilot song is a `queue_items` row with `autopilot` set: a `queue.AutopilotInfo` as JSON, saying which song seeded it. `added_by` still names a user (the column is a foreign key), but that's *whose taste seeded it*, not who chose it. Everywhere that matters, the song is treated as nobody's:

- It isn't in that user's lane. `ListLane` and `NextLanePosition` skip it, and nobody can move it.
- It plays after every member's song. The fairness engine never sees it: `SnapshotTx` orders the members' songs, then adds autopilot's in the order they were added. A member's song always goes first, even one added while autopilot's is up next.
- It doesn't take anyone's turn. `LastPlayedByUser` leaves it out. `RecentPlayers` reports it as `''`, so it still counts as "another song" for the cooldown.
- It isn't credited in stats or sessions, though it counts in the room's totals.
- Nobody skips it as its owner. The room's skip permission applies, and in a voting room everyone can vote, including whoever's taste seeded it.
- Anyone may remove it.

Making `added_by` nullable would say "nobody's" more directly, but SQLite can only do that by rebuilding `queue_items`, which `play_history` references. The marker column is a cheap migration, and `QueueItem.IsAutopilot()` keeps the checks in one place.

### When it fills

`autopilot.Service.Kick` runs after every queue change (chained after the playback engine's `OnChange`) and after room settings change. On a background goroutine, one per room at a time, it adds **one** song if:

- the room has autopilot on,
- no song is waiting (the one playing doesn't count, so the next song is queued while the last one plays and the speaker can preload it),
- the room has a speaker, and
- the room has played something to go on.

Adding one song at a time keeps autopilot close to the room: a new member song, a skip or a removal changes the next pick. If nothing new turns up, it posts a notice once and waits for the room to play something new before trying again.

### Seeds take turns

Seeds are the members' last 25 songs that played through (skipped and failed songs don't seed). Members take turns. People in the room go first, then whoever autopilot chose for longest ago (or never), then whoever played most recently. Autopilot tries up to three members and two seeds each. If none of those leads anywhere new, it follows its own last song, and as a last resort it plays a random song from a library in the room.

### Songs come from libraries in the room

`provider.Recommender` is an optional session interface with the `Recommendations` capability:

- `SimilarToTrack`, `SimilarToArtist`, `TopTracks`, `RandomTracks`.
- Navidrome maps these to `getSimilarSongs`, `getSimilarSongs2`, `getTopSongs` and `getRandomSongs`. Its metadata agents (Last.fm, when the server has a key set up) do the recommending and return only songs the server has, so every pick plays.

Spotify doesn't implement it: its recommendations API is closed to new apps.

Autopilot uses the recommending links of the people in the room and the seed's member, plus shared ones. It tries the link the seed played from first, then the seed member's own. A seed from another service (a Spotify song in a room with a Navidrome library) is found on the library by `match.On`, or by its artist's name.

The link wrapper in `links` implements `Recommender` on every session, the way it implements `PlayChecker`. That avoids doubling its interface combinations. A session that can't recommend returns `ErrUnsupported`, and autopilot only uses links whose provider declares the capability.

### Adventure

- **Similar** (default): `SimilarToTrack`, plus the artist's `TopTracks`. It picks at random from the top five fresh songs, and the seed's artist is fair game. If the service knows nothing similar (Navidrome without Last.fm), it plays more by the same artist from the library.
- **Discovery:** `SimilarToArtist`, with the seed's artist and the artists of the last five songs left out. It picks from anywhere in the list.

### Fresh songs, and listening to the room

A song autopilot won't pick:

- anything in the room's last 200 plays,
- anything waiting or playing,
- anything among its own last 50 songs, including the ones someone removed.

Songs are recognized by service and ID, by ISRC, or by artist and title. The room's repeat guard still applies when the song is added. When the room skips one of autopilot's songs (not a failure), autopilot avoids that artist for its next 20 songs.

## Consequences

- Autopilot is only as good as the library's agents. A Navidrome server without a Last.fm key gets "more by the same artist", then random songs.
- ListenBrainz radio, resolved back to songs a linked library has, could be another `Recommender` behind the same interface later.
- `added_by` on an autopilot song is a credit nobody should read as "queued by". New code that groups songs by person must check `IsAutopilot()`, or `QueueItem.autopilot` in the API.
- The per-room single flight is in-process, like the queue's lock (ADR 0002).
