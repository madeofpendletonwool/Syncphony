# ADR 0016: Syncphony playlists, and the night's Wrapped

- **Status:** accepted
- **Date:** 2026-10-09
- **Issues:** MAD-737, MAD-722

## Context

MAD-737 asked to export a night's songs to a playlist on someone's service (Navidrome, Spotify) when the night ends. A night mixes everyone's services, though, so an export to one service has to match every other service's songs on it first, and loses whatever it can't match. What people want is to keep the night and play it again, with the group. That doesn't need a service at all.

MAD-722 asked for a shareable end-of-night recap: song of the night, who brought what, genres and decades, whose tastes met, as animated story pages, an image to share, and a version for the big screen.

## Decision

### Playlists live in Syncphony

`playlists` and `playlist_songs` (migration 00028). A song is kept the way a queue item is: provider, link, track ID and the track's snapshot. So one playlist can hold songs from everyone's services, and plays each from the service it came from, under the same rules as "add again": the link has to be yours or shared with you, unless the room lets people borrow (`cant_borrow` otherwise). A song whose service was unlinked still shows, but can't be queued.

- **Owned, and shared with a room.** A playlist belongs to whoever made it. Shared with a room (`room_id`), everyone who can enter the room sees it and can queue from it. Only its owner, or an admin, changes it. Guests have no playlists.
- **Made two ways.** Saved from a night (`POST /rooms/{id}/playlists` with the recap's range), or made empty and filled from search, the queue or history (`POST /playlists/{id}/songs`). A saved night keeps the songs that played, in play order, and who queued each one. Skipped songs and repeats are left out unless kept. It defaults to shared with the room, and remembers the night it came from, so the night's recap can link to it.
- **Queued like anything else.** `TrackToQueue.fromPlaylistSongId` puts a playlist song in your lane, through the same add as search, with the same repeat guard, duplicate warning and fairness.
- **Covers through the playlist.** A song's artwork loads through its own link (`/playlists/{id}/songs/{songId}/artwork`), like a queued song's, so the room sees covers from links that aren't theirs.
- **Limits.** 1000 songs in a playlist, 100 per add. Positions are dense from 0 and rewritten on each move; at this size that's a few hundred updates at most.

Exporting a playlist to a service stays a separate, later piece of work. It'll start from these playlists, rather than from a night.

### The Wrapped is computed on read

`GET /rooms/{id}/recap?from&to` adds to a range's stats (ADR 0005): the top adder, whose songs got the most hearts, the song skipped most, the longest run of one person's songs played through without a skip, the genre and decade mix, pairs of friends who brought the same artists, the night that ended in the range (with its song of the night) and the playlists saved from it. `internal/recap` is pure, like `stats`.

Genres and years come from the music knowledge cache (`musicgraph`, ADR 0012): an artist's two strongest tags, by weight, and a song's year from its service or the cache. Only what's already cached is used. Songs are warmed into it when they're queued, so by the end of a night most are there, and a recap never waits on Last.fm. A mix can be empty.

### Story pages, an image, and the big screen

The web app shows the recap as a story: one page per thing worth telling, tap to go on or back, hold to pause. It's opened from a session in History → Recaps, or from the crowning when a night ends. The last page shares an image card, drawn on a canvas at 1080×1920 (Web Share where the device has it, a download otherwise), and saves the night's playlist or opens the one saved.

On the big screen, the Wrapped plays by itself once the crowning is done, and ends on a QR code for the night's playlist when one's been shared with the room. Displays may call `getRoomRecap`.

## Consequences

- A **Library** tab joins the bottom navigation.
- A playlist is only as playable as the links its songs came from. Matching songs onto your own service (MAD-704) would make other people's songs playable without borrowing; until then, the playlist page says how many songs you can't queue there.
- The recap reads up to 100 nights to find the one in its range, and the musicgraph cache once per artist and song.
