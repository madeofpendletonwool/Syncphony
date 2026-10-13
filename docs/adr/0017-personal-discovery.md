# ADR 0017: Personal discovery

- **Status:** accepted
- **Date:** 2026-10-13
- **Issues:** MAD-814 (Phase 12), stage 1: MAD-815..819

## Context

Phase 12 adds a way to find music you **don't have yet**, the way Google Play Music's "new releases for you" did. Everything stays on the server. You hear a 30-second preview, follow a link out to YouTube for the whole song, and later (stage 4) keep what you like on a Want list until it turns up in your library.

The obvious seed is your library. It's the wrong one. A Navidrome library holds plenty of music its owner doesn't love, so recommending from it recommends the wrong things. Syncphony's own history is still short, too.

## Decision

### Library ≠ taste

- **Taste comes from seed sets you build by hand.** A set is a named list of artists ("Moody post-rock", "90s alt"). Each one is its own feed. A seed can be any artist, not just one in your library: artist search asks Deezer, and the name as typed seeds as well as any.
- **The library is only a filter.** `internal/library` indexes the songs on each user's **own** links that can list their whole library (Navidrome's, through the optional `provider.Cataloger`). Shared links and Spotify aren't indexed, since what's on your own server is what you have. Anything there is left out at song level (ISRC, MBID, or artist and title as `internal/match` normalizes them) or release level (artist and album). It's never filtered at artist level, so a new album by an artist you own still shows. `KnowsArtist` is there for showing (the stage 2 map dims owned artists), never for filtering.
- **The index** lists the library with `search3` and an empty query, 500 songs a page. That's a request per 500 songs, where walking artists and albums takes one per album. It's kept in memory per link, for lookups well under a millisecond, and in SQLite (`library_indexes`, gzipped keys) so a restart doesn't rebuild it. A link is first indexed when its user opens Discover. After that it's rebuilt daily, or on demand. A rebuild is skipped while Navidrome's `getScanStatus.lastScan` hasn't changed. Builds run one at a time, in the background.

### Sets are personal

Migration 00029 adds `discovery_sets` (explore 0–100, default 40; mode `deep`, `fresh` or `both`), `discovery_set_artists` (unique on the set and the artist's key: their MBID, else their simplified name), `discovery_feedback` (one thumb per user and song; the latest wins), `discovery_blocks` (the global "not for me") and `want_items` (filled from stage 4 on; the feed already leaves them out). Every query takes the user. Another user's set is `not_found`, and guests get `forbidden`. Shared sets come later.

### The feed

`internal/discover` walks the music graph (`musicgraph`, ADR 0012) out from a set's seeds. It works the way the DJ's walk does (`dj/candidates.go`), but it's its own walk. The DJ's is built around a room's members and what they've heard. This one starts from seeds, and it inverts the DJ's "playable on your links" rule into "not in your library".

- **Overlap.** An artist's pull is the sum of how near it is to each seed, raised by half for each seed past the first (×1.5 near two seeds, ×2 near three). An artist near three seeds beats one that's very near just one. A set with explore at 35 or more takes a second step, damped, from the nearest new artists.
- **Songs.** The 40 nearest artists' top songs, plus the seeds' own songs at half pull (deep cuts). Live takes, remixes and demos are dropped. So are songs you have, turned down or want, and artists you blocked.
- **Popularity.** Deezer's rank and fan counts make a song's popularity. At a low explore, popularity counts for a song. At a high one it counts against it, so the feed leans toward less-known artists.
- **Variety.** The feed is split into pages of 20. On each page the seeds take turns, each giving its best song left, and no artist has more than two songs, as with suggestions (ADR 0010).
- **Why.** Each song names the seeds it's near: "Near Slowdive and Hum".
- **Bounded.** A build takes at most 15 seconds. It fetches up to 12 artists the graph hasn't cached, seeds first, and warms the rest for next time. A built feed is kept 10 minutes per set, and rebuilt sooner if the set changes or you ask (`refresh=true`). What you have, turned down or blocked is checked again as each page is served, so a thumbs-down leaves every page at once.

### Previews through the server

Deezer's 30-second MP3s are signed and expire, so their URLs are never stored. `GET /me/discover/preview/{deezerTrackId}` asks Deezer for the track each time and proxies the MP3 with byte ranges, the way songs are proxied. It uses the same Deezer client and rate limit as the music graph, and only accepts audio from Deezer's CDN. Covers and artist pictures come through `GET /me/discover/images/{kind}/{imageId}` the same way. The browser never talks to Deezer.

Top songs from Deezer carry their Deezer ID, album and cover (`musicgraph.SongRef`). A song that only another source listed is looked up on Deezer once (`artist:"x" track:"y"`, then a looser search) when its page is first served. The result goes in `deezer_songs`, a miss included, and a miss is looked up again after 30 days. A song with no preview still offers the YouTube link.

### The full song: a YouTube search link

"Open on YouTube" links to `https://www.youtube.com/results?search_query=<artist> <title>` in a new tab. It needs no API key, no embed and no yt-dlp. There are no store or buy links.

### Feedback stays out of rooms

Discovery thumbs and blocks never reach a room's DJ (`dj_affinity`), and a room's skips never touch a set. Stage 1 stores thumbs-up. Stage 4 turns them into Want-list entries and growing sets.

## Consequences

- A **Discover** tab joins the app shell, outside any room. Its preview player plays locally, one song at a time, and leaves the room's playback and Media Session alone.
- Discovery needs at least one music graph source for feeds, and Deezer (on by default) for previews and artist pictures. Without a source, feeds say so (`noGraph`).
- An index holds keys, not songs: tens of bytes a song. A 50,000-song library is a few MB in memory, and less on disk.
- Until a user's library is first indexed, the feed can show songs they have. The tab shows indexing as it happens.
