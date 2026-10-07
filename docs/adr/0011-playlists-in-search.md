# ADR 0011: Playlists in search results

- **Status:** accepted
- **Date:** 2026-10-07
- **Issue:** MAD-745

## Context

Playlists were browsed, not searched: each link's playlists showed as a shelf before you typed, and search returned songs, albums and artists only. With a long list of playlists, or a few services' worth, finding one meant scrolling shelves. People also want to find playlists that aren't theirs ("chill", "90s rock") on services that have a public catalog of them.

Services differ. Subsonic's `search3` doesn't search playlists at all; Navidrome's `getPlaylists` lists the account's own and other users' public ones. Spotify's Web API searches everyone's public playlists, but can't list or read a user's library (ADR 0004); that comes over the streaming login.

## Decision

### Your playlists, matched by name on the server

For every link whose service lists playlists (`Capabilities.Playlists`), `GET /search` lists them (the first page, which is all of them for the services we have) and keeps those whose names hold every word searched for. A word matches the start of a word in the name, ignoring case, accents and punctuation (`match.Simplify`), so "chill v" finds "Chill Vibes" as you type. They're listed in the service's order, which for Spotify is most recently played first.

Listing happens alongside the service's search, under the same 8-second bound. If it fails, the link's other results still stand. A link's list is cached in memory for a minute, since a search is asked again every few letters typed and listing Spotify's takes several requests. A playlist added in that minute shows up after it.

### Public playlists, when asked

`GET /search?publicPlaylists=true` also asks services that declare `KindPlaylist` in `Capabilities.Search` to search playlists. Spotify does when it has the streaming library, since that's how a found playlist's songs are read; Navidrome and nugs.net don't (Navidrome's public playlists already come with your own). A playlist found both ways is listed once.

Spotify's development-mode Web API keeps changing what it returns: playlist results can be `null`, and a playlist's length moved from `tracks` to `items` in February 2026. Both are handled. Spotify's own editorial playlists may not be returned to new apps at all.

### On the Search screen

Results get a **Playlists** section and tab. Where a searched service can look through public playlists, a **Public** switch sits on the section (and the tab). It's remembered on the device, like recent searches. A playlist opens on the usual playlist page; one that isn't in the link's list gets its name and cover from the search result, carried in the navigation's history state.

## Consequences

- Searching costs one more request per link with playlists, at most once a minute.
- A public playlist opened from a reload of its page keeps its name only as long as the browser keeps the history entry; a link to it pasted elsewhere shows "Playlist" and the first song's cover.
- The `SearchGroup` schema gains a required `playlists` array.
