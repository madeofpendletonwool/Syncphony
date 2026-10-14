# ADR 0017: Personal discovery

- **Status:** accepted
- **Date:** 2026-10-13
- **Issues:** MAD-814 (Phase 12), stage 1: MAD-815..819, stage 2: MAD-820, 821, stage 3: MAD-822, 823, stage 4: MAD-824..826

## Context

Phase 12 adds a way to find music you **don't have yet**, the way Google Play Music's "new releases for you" did. Everything stays on the server. You hear a 30-second preview, follow a link out to YouTube for the whole song, and keep what you like on a Want list until it turns up in your library.

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

### The map

`GET /me/discover/map` (MAD-820) draws a graph in the spirit of music-map.com, around any artist (`artist`, with `mbid` if known) or around one of your sets (`setId`).

- **Around an artist** (depth 2 by default): half the room goes to its nearest neighbors, the rest to their own nearest (four each, damped as the feed's second step is), then more neighbors if that's not enough.
- **Around a set** (depth 1 by default): the seeds, and the union of their neighbors, the ones near the most seeds first, then the nearest.
- **Bounded:** 60 artists by default (10 to 100). Each artist draws only its three strongest likenesses, so sixty artists don't make a hairball. The graph's merged score sets how alike they are.
- **Fast once warm:** the map reads the music graph's cache. It fetches at most three artists that aren't cached (the artist it's of, or the first seeds), and waits for them at most 5 seconds. A fetch still going after that carries on in the background and is cached. Everyone else who isn't cached is warmed, and the map says it's `partial`, so the page loads it again a few times as it fills out. A warm map takes tens of milliseconds.
- **What each artist says:** whether you have anything of theirs (`KnowsArtist`: for showing only), whether they're a seed or the center, how many seeds they're near, their Deezer fans, picture and top tags, and whether the graph knows them yet. Artists you blocked are left out, unless the map is of them.

`GET /me/discover/songs?artist=` gives an artist's best-known songs that you don't have, haven't turned down and don't want yet, with previews. It's for the sheet that opens when you tap an artist on the map.

The **Map** view (MAD-821) is a tab beside a set's feed, and `/discover/map?path=…` for any artist. It's laid out with d3-force, all at once off screen. Distance follows how alike two artists are, and size follows how well known they are. It's drawn in SVG, and a new layout starts from where the last one left each artist, then tweens there, so re-centering glides. Artists you have are dimmed, new ones are in full color, and seeds and the center are ringed. In a set's map, artists near several seeds get a soft halo. Each re-center is a step in the URL, so Back and the breadcrumbs retrace them. There's nothing beat-reactive: the app shell stays calm.

### Fresh releases

The **Fresh** feed (MAD-823) is the "new releases for you" part: what your seeds and their neighbors put out lately. `internal/releases` (MAD-822) finds an artist's recent releases from three sources and merges them by artist and title (`library.ReleaseKey`, so a deluxe edition is the same release):

- **ListenBrainz's fresh releases** (`/1/explore/fresh-releases`, 90 days): every release lately, about 12 MB, read once every 6 hours and kept in memory by artist MBID and name. One request answers for every artist. It carries release group MBIDs, types and Cover Art Archive covers.
- **MusicBrainz release groups** for an artist's MBID, searched by first-release date: one request, through the server's shared one-a-second client, retried once if MusicBrainz says it's busy. A date that's only a year doesn't count, since it can't say whether something is recent.
- **Deezer's albums** for the artist: two requests. They carry a cover and the Deezer ID that a release's songs and previews come from. Deezer dates a reissue or deluxe edition to when it came out, so each release counts from its earliest edition on Deezer. Otherwise an old album would look new.

The first source to list a release says what kind it is: MusicBrainz's types (`album`, `ep`, `single`, `compilation`, `live`) say more than Deezer's. Then the most precise date wins, and any source can fill in an ID or a cover. An artist's releases from the last 100 days are cached a day in `artist_releases` (migration 00030), or an hour if a source failed. Every 6 hours, the seeds of every set, then their ten nearest neighbors, are warmed one artist at a time, so the sources' limits hold.

A set's Fresh feed (`GET /me/discover/sets/{setId}/fresh`) looks back its `fresh_days`: 30, 60 or 90, default 60. It takes the seeds and their nearest neighbors (15 of them, up to 50 for a set that explores) from the map's walk. Ranking:

- Compilations and live albums come last.
- Ahead of them, the seeds' releases come first, then albums and EPs before singles.
- Within those, the score decides. For a neighbor, that's how near it is, with the same explore lean on how well known it is as the song feed, then the kind of release, then how recent it is.

There are at most 3 releases per artist and 100 in all. Releases you have (`HasRelease`, by the artist found and the artist credited), want, or by an artist you blocked are left out.

Like the map, it reads the cache. It fetches up to four seeds' releases that aren't cached, within 6 seconds, and warms the rest. Then it says it's `partial`, and the page loads it again a few times as it fills in.

A release card shows its cover (Deezer's, else the Cover Art Archive's through `GET /me/discover/release-art/{mbid}`), its kind and date, and the seeds it's near. Opening it lists its songs from Deezer (`GET /me/discover/release-tracks`, by Deezer ID or found by title), each with a preview and a YouTube link. The card also has an "Open on YouTube" search for the whole release. In **Both** mode, the web app mixes Fresh cards into the deep-cuts feed with a **New** badge: one after the first song, then one every four. That keeps the song feed's pages as they are.

The unread dot on the Discover tab, for releases you haven't seen yet, isn't built. Knowing what's unseen would mean working out every set's Fresh feed for the tab bar.

### The Want list

A thumbs-up on a song, or **Want** on a release, puts it on the Want list (`want_items`), noting the set whose feed it came from (MAD-824). Wanted things are left out of every feed and Fresh feed. Syncphony never gets them for you. You follow the YouTube link, add what you like to your Navidrome, and Syncphony notices.

- **Automatic got-it.** `library.Service` calls its `Built` hook after every index build of a link, with the link's user. That includes a build skipped because Navidrome hadn't rescanned, since something wanted since then may already be there. `discover.CheckWants` then matches each wanted thing against the user's index: a song with `Has` (ISRC or MBID first, then artist and title), a release with `HasRelease` (artist and title, so a deluxe edition counts). A match is marked `got`, `got_by = auto`, with `got_at`. Discover then says "3 wanted things landed in your library" until you open the Want list (`got_seen`). Marking something got yourself (`got_by = manual`) doesn't count as landing.
- **The screen** (`/discover/want`) groups by artist, with Wanted, Got and Dismissed. A wanted song plays its preview and links to YouTube, and anything can be marked got or dismissed. Got releases link to the album in Syncphony (the linked-names lookup, ADR 0013 and MAD-837), and got songs to a song search, to queue or play right away. There are no store links. Wanting a dismissed thing again brings it back.
- **What a set yields.** The set list counts what each set's feeds led you to want, and how much of that you got (`wanted`, `got` on `listDiscoverySets`). That shows which sets pay off. The counts follow `want_items.set_id`, which is cleared when a set is deleted.

Migration 00032 adds `want_items.cover`, `got_by` and `got_seen`.

### Sets that learn

Thumbs still never reach a room (MAD-825). Within discovery, they now do three things:

- **Growing.** An artist you thumbed up twice from a set's feed is offered for the set ("You liked 2 songs by Failure. Add them?"). A set with **Grow by itself** on (`grow_auto`, off by default) adds them at the second thumbs-up, and the thumb's answer says who joined (`grew`). Either way they're stored with `added_from = thumb`. Offers you turn down are kept in `discovery_dismissed` and aren't made again.
- **Thumbs-down.** Each thumbs-down from a set's feed lowers that artist's songs in that feed by 0.6×, so three take them to about a fifth. That happens when the feed is next built, within 10 minutes or on refresh, not on every thumb, because a rebuild mid-scroll would restart the pages. The song turned down still leaves every feed at once. Three thumbs-down for one artist, in any feeds, offers to block them.
- **Not for me** is the global block list. It and the library's status now live on a Discover settings screen (`/discover/settings`), where you can unblock.
- **Starred import** is a one-time, opt-in pick list. `GET /me/discover/starred` lists the artists you starred on your own Navidrome links, and the artists of the albums and songs you starred, through a new optional `provider.Starrer` (Navidrome's `getStarred2`). Starred artists come first, then by how much of theirs you starred. Only the ones you check are added (`importDiscoverySetArtists`, `added_from = starred`). It's never automatic: starring something doesn't make it a set's taste.

`importDiscoverySetArtists` adds the artists you picked at once, from a suggestion, your stars or a room. Ones in the set already are skipped. More than the set has room for is `too_many`, and adds none.

### A room's taste as a source

A set can draw on a room you can open (`rooms.CanEnter`: open rooms, yours, or ones you're a member of) in two ways (MAD-826):

- **Snapshot.** `GET /me/discover/room-taste` lists a room's favorite artists, or one past night's (`nightId`), optionally from only your own picks (`mine`). You check the ones to keep, into a set or a new one, with `added_from = room`. A room's taste is its long-term taste as the DJ keeps it (`dj.Peek`) merged with a profile of its latest 200 plays (`dj.NewProfile`), whichever likes an artist more. Artists the room turned away lately are left out. A night's taste is that night's plays alone (`dj.NightInput`). With `mine`, the long-term rows are narrowed to your own (`dj_affinities.member`), and the profile to your share of each artist (`ByMember`).
- **Live.** A set can link to one room (`discovery_sets.room_id`) with a weight: low 0.3, medium 0.55 or high 0.85. Building the feed then walks out from the room's 12 favorite artists that aren't seeds, as from seeds that pull that much less, times how much the room likes each one. As the room's taste moves, so does the feed, at its next build. A set linked to a room can have no seeds of its own. Fresh feeds and maps still follow only the seeds.

Both are read only. `dj.Peek` reads the DJ's stored rows without folding in nights that ended since, which `Memory.Load` would do, so discovery never writes a room's DJ memory. Room skips don't affect a set. A room you can no longer open is dropped from the walk, and `room_id` is cleared if the room is deleted.

### Feedback stays out of rooms

Discovery thumbs and blocks never reach a room's DJ (`dj_affinity`), and a room's skips never touch a set. Reading a room's taste for a set doesn't change it either.

## Consequences

- A **Discover** tab joins the app shell, outside any room. Its preview player plays locally, one song at a time, and leaves the room's playback and Media Session alone.
- Discovery needs at least one music graph source for feeds, and Deezer (on by default) for previews and artist pictures. Without a source, feeds say so (`noGraph`).
- An index holds keys, not songs: tens of bytes a song. A 50,000-song library is a few MB in memory, and less on disk.
- Until a user's library is first indexed, the feed can show songs they have. The tab shows indexing as it happens.
