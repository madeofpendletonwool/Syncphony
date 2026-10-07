# ADR 0013: Artist, album and genre pages

- **Status:** accepted
- **Date:** 2026-10-07
- **Issue:** MAD-763

## Context

An artist's page showed what their service returned: a picture, a name and albums. People browsing a jukebox expect more: who the artist is, their hits, what's like them. The server already knew much of it. Liner notes read Wikipedia bios through MusicBrainz (MAD-717), and the music knowledge layer (ADR 0012) knows artists' top songs, similar artists and tags.

## Decision

### What's known says what to show; the link says what plays

Artist and genre pages are asked for through one link (`/links/{id}/...`), like the artist itself. The `artistpage` package takes what's known about music and looks it up on that link: one search for the artist's songs, matched against their top songs with `match.Score`, then a few one-by-one lookups (`match.On`) for top songs that search missed. Similar artists and a genre's artists cost one search each (artists and songs together), a few at a time. Only songs found on the link are shown, so everything on the page can be added.

When nothing knows an artist's top songs, the service's own top tracks stand in (`Recommender.TopTracks`), then what its search finds first. When nothing knows who's alike, the service's `SimilarToArtist` does.

### Notes

`linernotes` writes artist notes (Wikipedia bio, began/ended facts, members, MusicBrainz genres, release groups) and album notes (release type, first release, labels, genres, Wikipedia summary). They're cached in `page_notes_cache` with the same TTLs as songs' notes. Genres on a page are MusicBrainz's curated ones, then tags the knowledge layer has with weight 0.2 or more, skipping tags that aren't genres ("seen live").

### Discography by kind

`provider.Album.Kind` says whether a release is an album, single, EP, compilation or live, where the service says: Navidrome's OpenSubsonic `releaseTypes`, Spotify's `album_type` (which calls EPs singles). For the rest, the artist's MusicBrainz release groups are matched by title, without edition notes. The page splits albums into Albums, Singles & EPs, Live and Compilations.

### Genres

A genre's top artists come from Last.fm's `tag.getTopArtists`, cached in `musicgraph_tags`. Without Last.fm, the artists already cached with the tag stand in, kept for an hour. Genre pages need the knowledge layer; without it they're not found.

### Across services and rooms

`/elsewhere` finds the same artist by name on your other usable links. The page links to each and merges their albums into the discography, each once by title, tagged with its service. "Popular in <room>" lists the artist's songs that played to the end most in the current room (`stats.ArtistTop`), to add again. Shuffle adds songs at random from up to six of the artist's albums, spread across them. The fair queue keeps a long add from crowding others out.

## Consequences

- The first visit to an artist can take several seconds (MusicBrainz's rate limit, the knowledge layer's sources); sections load on their own, so the page shows what the service knows at once.
- An artist with a common name may be matched to the wrong MusicBrainz artist, as liner notes can be.
- Lyrics previews are left out.
