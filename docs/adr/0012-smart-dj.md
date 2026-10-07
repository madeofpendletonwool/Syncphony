# ADR 0012: Smart DJ: music knowledge apart from the services

- **Status:** accepted (stage 1 of Phase 7)
- **Date:** 2026-10-07
- **Issues:** MAD-750 (Phase 7), MAD-751, MAD-752

## Context

Autopilot (ADR 0008) and suggestions (ADR 0010) find songs through `provider.Recommender`, and only Navidrome implements it. Navidrome's similar songs come from its own Last.fm agent, so:

- A Navidrome server without a Last.fm key gets "a random album by the same artist", then random library songs.
- Spotify and nugs.net add nothing, so a room without Navidrome has no autopilot at all.
- Nothing says which of an artist's songs are their hits, or how a song sounds (tempo, era).

The knowledge of how music relates shouldn't depend on which service plays it.

## Decision

### What should play is separate from where it plays

A new package, `internal/musicgraph`, knows about artists and songs by name, MusicBrainz ID and ISRC, never by a service's own IDs. It answers:

- **About an artist** (`Artist`): similar artists with a 0–1 score, their top songs with a 0–1 popularity *among their own songs*, and tags.
- **About a song** (`Track`): similar songs, BPM, the year it first came out, and how popular it is across all music.

Stage 2 (MAD-753..755) turns those into picks and finds them on the room's services with `match.On`, as suggestions already do for a seed. When autopilot moves onto it, this ADR replaces ADR 0008's "Songs come from libraries in the room". Until then nothing reads it but the cache warmer, so autopilot's behavior doesn't change yet.

### Sources

Each source is optional and implements `musicgraph.Source`. A source answers what it can, and leaves the rest empty.

| Source | Artist | Song | Needs | Rate |
|---|---|---|---|---|
| **Last.fm** | `artist.getSimilar` (match score), `artist.getTopTracks` (plays), `artist.getTopTags` | `track.getSimilar` | `SYNCPHONY_LASTFM_KEY` | 5/s |
| **ListenBrainz** | labs similar-artists (the session-based dataset LB Radio uses), `popularity/top-recordings-for-artist` (listens) | — | the artist's MBID; a token optional | 4/s |
| **Deezer** | `search/artist` → `related`, `top` (rank) | by ISRC, else search + `match.Score`: BPM, rank, release date | nothing | about 8/s, under its 50 per 5s |
| **MusicBrainz** | genres and tags | `first-release-date` | MBIDs; shares the server's 1/s | 1/s |

An artist's MBID, which ListenBrainz and MusicBrainz need, is found with `musicbrainz.FindArtist`: the best search result with the same simplified name.

Spotify's related-artists, recommendations and audio-features APIs are closed to new apps (Nov 2024), so Spotify isn't a source.

### Merging

Every source's scores are 0–1:

- Last.fm's match is already 0–1.
- ListenBrainz's similarity is divided by its top score.
- Deezer's related artists, which come only as a ranked list, score from 1 down to 0.5 by position.

Plays and listens become popularity as `sqrt(count / top count)`. A song with a quarter of the hit's plays scores 0.5, and a deep cut with 1% scores 0.1. A log scale would squeeze a big artist's whole catalog into 0.95–1, and a plain ratio would push everything but the hit toward 0. Deezer's rank is already compressed, so it's compared as a plain ratio.

The merged score is the weighted average over the sources that answered. A source that answered but didn't list something counts as 0 for it, so what more sources agree on ranks higher. Weights: Last.fm 1, ListenBrainz 0.8, MusicBrainz 0.8, Deezer 0.6. A song's year prefers MusicBrainz's first release over Deezer's release date, which is often a reissue's.

### Cache

- Answers are cached in `musicgraph_artists` and `musicgraph_tracks`, as JSON.
- Artists are keyed by simplified name and are also findable by MBID. Songs are keyed by simplified artist and title.
- Answers are kept 7 days. Misses are kept too, for 1 day, so an unknown artist isn't asked about on every fill.
- An answer some source failed to give, including a failed MBID lookup that kept ListenBrainz and MusicBrainz out, is also kept only 1 day.
- An outage of every source isn't cached.
- Simultaneous fetches of the same artist or song are combined into one.

### Warming

`queue.OnAdd` hands queued tracks to `Warm`. One background worker fetches each track's artist and song, then the artist's 5 nearest neighbors. It stops after one hop, so a walk of the graph in stage 2 finds the next step cached without the warmer spreading out across the whole graph. Fills read the cache (`CachedArtist`, `CachedTrack`) and don't wait on the network.

### Configuration

| Variable | Default | |
|---|---|---|
| `SYNCPHONY_LASTFM_KEY` | unset (off) | free at last.fm/api |
| `SYNCPHONY_LISTENBRAINZ_URL` | `https://api.listenbrainz.org` | `off` turns it off |
| `SYNCPHONY_LISTENBRAINZ_TOKEN` | unset | optional |
| `SYNCPHONY_DEEZER_URL` | `https://api.deezer.com` | `off` turns it off |

MusicBrainz joins when `SYNCPHONY_MUSICBRAINZ_URL` isn't off. `TestLive` in `internal/musicgraph` asks the real services when `SYNCPHONY_TEST_MUSICGRAPH_LIVE=1` is set.

## Consequences

- The artists and titles of queued songs go to Last.fm, ListenBrainz and Deezer, as they already go to MusicBrainz and LRCLIB. ListenBrainz and Deezer are on by default; each can be turned off.
- Without a Last.fm key there are still similar artists and top songs, from ListenBrainz and Deezer, but no similar songs. Last.fm is the strongest single source.
- ListenBrainz's labs API is slow and sometimes times out. That costs only its part of an answer, for a day.
- The similar-artists dataset name is ListenBrainz's own and may change. If it does, ListenBrainz stops contributing similar artists until the constant is updated, and the other sources carry on.
- An artist's name can belong to more than one artist. Deezer takes the one with the most fans, and MusicBrainz the best search result.
- Tempo is often unknown: Deezer has a BPM for some songs and 0 for many. Set flow (MAD-757) must treat 0 as unknown, not slow.
