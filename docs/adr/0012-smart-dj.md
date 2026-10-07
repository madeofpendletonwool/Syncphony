# ADR 0012: Smart DJ: music knowledge apart from the services

- **Status:** accepted (stages 1 and 2 of Phase 7, and learning from stage 3)
- **Date:** 2026-10-07
- **Issues:** MAD-750 (Phase 7); stage 1: MAD-751, MAD-752; stage 2: MAD-753, MAD-754, MAD-755; stage 3: MAD-756, MAD-762

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

Stage 2 (`internal/dj`, below) turns those into picks and finds them on the room's services with `match.On`. This replaces ADR 0008's "Songs come from libraries in the room", "Seeds take turns" and "Adventure". ADR 0008's rules on when autopilot fills, and on autopilot songs being nobody's, still hold.

### Sources

Each source is optional and implements `musicgraph.Source`. A source answers what it can, and leaves the rest empty.

| Source | Artist | Song | Needs | Rate |
|---|---|---|---|---|
| **Last.fm** | `artist.getSimilar` (match score), `artist.getTopTracks` (plays), `artist.getTopTags` | `track.getSimilar` | `SYNCPHONY_LASTFM_KEY` | 5/s |
| **ListenBrainz** | labs similar-artists (the session-based dataset LB Radio uses), `popularity/top-recordings-for-artist` (listens) | — | the artist's MBID; a token optional | about 3/s, and its `X-RateLimit-*` headers (30 per 10s) |
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
- **Throttling.** A throttled request (429 or 503, Last.fm's error 29, Deezer's quota error) is waited out and tried once more. A source that sends `X-RateLimit-Remaining` and `X-RateLimit-Reset-In`, as ListenBrainz does, is left alone until its window resets once it's down to its last request. In the first production run, the warmer went past ListenBrainz's 30 requests per 10 seconds.
- **ListenBrainz without a token** sometimes refuses popularity with a 401, saying it gates anonymous use against scrapers. ListenBrainz's similar artists still count, and the answer isn't treated as a failure. The server logs once that a token would help.
- **Repair at startup.** The server fetches again every entry kept only for the 1-day miss TTL, oldest first, in the background. Those are entries some source failed to give, and misses. A fix to a source, or an outage that's over, then fills in the cache on the next start instead of a day later.

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

## The DJ (stage 2)

`internal/dj` picks songs, and `autopilot` asks it first. It knows nothing about queues or turns: it's given what the room did, and returns songs to try, best first. It doesn't import `suggest`, so suggestions can move onto it later (MAD-759).

### The room's taste (MAD-753)

`dj.Profile` is read from the room's last 100 plays, the songs playing and waiting, and autopilot's own recent songs.

- **Signals:**

  | The room… | Counts |
  |---|---|
  | let a member's song play through | +1 |
  | has a member's song playing or waiting | +0.8 |
  | let one of autopilot's songs play through | +0.5 (a throwback: +0.25) |
  | hearted a song | +0.4 a heart, up to 3 |
  | queued a song again within 3 hours of it playing | +0.6 |
  | queued a song by the artist of autopilot's song, or a similar one, while it played or within 5 minutes | +0.7 for autopilot's song's artist, from that member |
  | skipped a song (anyone's) in its first 10 seconds | −1.5 |
  | skipped it later | −1 just after 10 seconds, to −0.2 at the end |
  | removed one of autopilot's songs | −0.5 |

  Each signal halves every 2 hours. A song's length comes from its metadata, or is taken as 4 minutes. "Similar" is what the graph's cache knows.
- **Fair across members.** Each member's likes are scaled to the same total, however many songs they've queued. Members in the room count 1.5, members who left 1, and autopilot's songs 0.5. Weights are relative to the room's favorite artist, which scores 1.
- **Turned away.** An artist whose decayed net signal is below −0.25 (about one skip in the last two hours outweighing the room's liking) is left out entirely, both as a seed and as a pick. A skip long ago, decayed to almost nothing, no longer turns an artist away (MAD-762). Between −0.25 and 0 the artist is *in doubt*, and their songs lose up to 0.3. Autopilot's fallback uses the same rule; it replaced ADR 0008's "avoid that artist for 20 songs".
- **Tonight.** Artists liked so long ago that they weigh under 1% of the favorite drop out. Spacing counts only songs of the current session: the run since the room was last quiet for 2 hours (`stats.SessionGap`).
- **Heard.** Songs the room heard or has waiting are compared by artist and title without qualifiers, so a remaster or live take of a song the room just heard doesn't play.

### Candidates and finding them (MAD-754)

- **The walk** starts from the room's 8 favorite artists and follows up to 30 similar artists from each. Each artist it reaches collects `taste × similarity` from every path, and remembers the room artist that leads there most strongly ("via").
- **Two steps.** At explore 35 or more, it also goes two steps out from the 10 nearest new artists, with the second step counting 0.6.
- **Songs.** Candidates are the top 15 songs of the 40 nearest artists, plus up to 20 songs like each of the room's 3 latest songs that played through (Last.fm `track.getSimilar`). Live takes, remixes, demos and other variants are left out.
- **The cache first.** The walk reads the cache. It may fetch up to 6 uncached artists over the network within 12 seconds: the room's top 3 favorites first, then the nearest ones it reaches. The rest are warmed for the next fill.
- **Finding a pick.** Each drawn candidate is looked for with `match.On` on up to 2 of the room's services that can search tracks, Spotify and nugs.net included. The service its "via" song played from goes first, then services of people in the room. A song is skipped if it isn't found, isn't fresh, or is on an album the room heard in its last 5 songs; the DJ moves to the next candidate. A room with no Navidrome now gets autopilot.

### Scoring and picking (MAD-755)

- **Explore.** `explore` (0–100) replaces similar/discovery. Rooms set up before it read 25 for similar and 75 for discovery. The server keeps `adventure` in step for older clients and the fallback (`discovery` from 50). The web app shows it as a slider.
- **Score:**

  ```
  score = wSim·similarity + wPop·popularity + wNov·novelty − spacing
  ```

  - **Similarity** is the candidate's affinity relative to the nearest artist's.
  - **Popularity** is the song's popularity among its artist's own songs.
  - **Novelty** is 0 for the room's artists, 0.6 one step out, 1 two steps out.
  - **The weights** move from (0.55, 0.40, 0.05) at explore 0 to (0.30, 0.20, 0.50) at 100.
  - **Spacing:** an artist heard in the last 3 songs loses 0.6, and one heard in the last 8 loses 0.25.
- **Deep cuts.** On 15% of fills, songs by artists the room has played 3 or more times have their popularity inverted, so a loved artist's lesser-known song can come up.
- **Picking.** The 25 best candidates, with at most 2 songs per artist, are drawn without replacement by softmax. The temperature runs from 0.05 at explore 0 to 0.15 at 100. The per-artist cap came from a live run: without it, one artist's catalog filled the whole shortlist.
- **Reasons.** Each pick stores its reasoning in `AutopilotInfo.reason`: the kind of relation, the via artist, similarity, popularity, novelty, score, whether it's a deep cut, and the sources. Explaining picks (MAD-760) reads it.

### Learning: present taste first, history as a weak prior (MAD-756)

A persisted affinity can drag a room back to an old vibe. A room on a week of Beatles shouldn't be pulled back to the Arctic Monkeys it played the week before. So the DJ keeps two layers.

- **Tonight's taste (`Profile`) drives the picks**, as above.
- **The long-term taste (`LongTerm`) is only a weak prior.** It raises a candidate's score by `prior · s/(1−s)`, with `s = priorShare = 0.12`, so it's never more than 12% of the score. History can tip a choice between near-equal candidates, but it can't outvote what the room is playing now. The prior is 0.6 × the room's long-term liking of the artist, plus 0.4 × its liking of the artist's tags (weighted by tag).

**Nights, not wall time.**
- `dj.Memory` folds each night once it ends. Nights are the ones `nights` already keeps, ended by the host or by `stats.SessionGap` of quiet.
- Folding a night first fades everything before it by 0.7, then adds the night's likes and skips. Those use the same signals as tonight's taste, without decay.
- Last weekend still counts and three months ago is faint, however many idle days fall in between. Wall-time decay alone would treat a week-long gap and a busy week the same.
- A room seen for the first time folds its last 30 nights.
- Folding happens when the DJ picks, or when the debug view is opened.
- It's stored in `dj_rooms` (nights folded, and when the last ended) and `dj_affinities`: likes and skips per room, artist or tag, and member, plus the night it was last liked.

**Fair.** Like the profile, each member's long-term likes add up to the same, so a member who's been around for months doesn't outweigh someone new. Autopilot's songs the room let play count 0.5.

**Long-term vetoes fade.**
- An artist's veto is `(skips − 0.5·likes) / 3`, clamped to 0–1, and costs up to 0.3 of a score.
- It fades with the nights like everything else, so it's a graded penalty that recovers, not a ban.
- A skipped throwback counts twice against its artist.

**Throwbacks: deliberate history.**
- On 10% of fills, the 3 artists the room loved most in past nights, but hasn't liked in its last 3 nights or played this session, compete as `throwback` candidates. They must weigh at least 0.03 against the room's long-term favorite, discounted by any veto, and weigh under 0.2 tonight.
- A throwback's similarity is its long-term liking against the most-loved throwback's, so the top one is as near as the room's nearest artist. It still has to win the draw.
- The chance halves for each throwback the room skipped in its last 100 plays. A quick skip also turns the artist away for the rest of the night, and counts double in their veto when the night is folded.
- A throwback's reason has kind `"throwback"`, `via` the artist, and `lovedAt`, when the night ended that the room last liked them. MAD-760 can then say "a throwback: the room played a lot of X in September".
- A throwback the room lets play counts half as much as other autopilot songs tonight, so one throwback doesn't start a run.

**A change of vibe.**
- Once the members have at least 13 songs, their 10 latest unskipped songs are compared with the rest of the room's reactions. The comparison is the cosine similarity of artist and tag vectors, with tags from the graph's cache.
- Under 0.35, everything since the 10th latest song counts 3 times as much, so the DJ follows the change instead of averaging it away.
- Once the night settles on the new vibe, the comparison is close again and the boost stops.

**Seeing it.** `GET /admin/rooms/{roomId}/taste` (admins, in rooms they can open) returns tonight's top artists, the artists turned away, whether the vibe is changing, the long-term artists (weight, veto, nights since last liked, throwback eligibility), and the long-term tags. The server settings page shows it under each room as "DJ's taste".

**Tests.** `scenario_test.go` builds a room with weeks of history in a real database: three weeks ago, a week of Arctic Monkeys; then a week of the Beatles; tonight, a few songs of something new. It checks the following:
- The picks follow tonight's songs, then the Beatles.
- The Arctic Monkeys come only as an occasional throwback, under 10% of picks and never twice within 5 songs.
- Skipping a throwback makes the next one rarer.

`TestLive` folds a multi-night history and logs a throwback fill.

### Fallback

If the graph has no sources, knows nothing near the room's taste, or none of its picks can be found, autopilot falls back to ADR 0008's chain, unchanged: the services' recommendations, then autopilot's own trail, then random songs.

`TestLive` in `internal/dj` (with `SYNCPHONY_TEST_MUSICGRAPH_LIVE=1`) walks the real graph for a sample room and logs the shortlist at explore 0, 50 and 100.

## Consequences

- The artists and titles of queued songs go to Last.fm, ListenBrainz and Deezer, as they already go to MusicBrainz and LRCLIB. ListenBrainz and Deezer are on by default; each can be turned off.
- Without a Last.fm key there are still similar artists and top songs, from ListenBrainz and Deezer, but no similar songs. Last.fm is the strongest single source.
- ListenBrainz's labs API is slow and sometimes times out. That costs only its part of an answer, for a day.
- The similar-artists dataset name is ListenBrainz's own and may change. If it does, ListenBrainz stops contributing similar artists until the constant is updated, and the other sources carry on.
- An artist's name can belong to more than one artist. Deezer takes the one with the most fans, and MusicBrainz the best search result.
- The first fill after a cold start knows only the artists it fetched in time. The warmer catches up from the songs being queued.
- `AutopilotInfo.reason` is stored with each song; it isn't in the API until MAD-760.
- A room's long-term taste is the room's: it's deleted with the room. It names members by ID, and keeps an artist only while its likes or skips are above 0.005, about 15 nights of silence for one play.
- Suggestions (ADR 0010) still use their own unfaded "turned away" rule until MAD-759 moves them onto the DJ.
- Tempo is often unknown: Deezer has a BPM for some songs and 0 for many. Set flow (MAD-757) must treat 0 as unknown, not slow.
