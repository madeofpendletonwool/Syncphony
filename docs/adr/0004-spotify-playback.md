# ADR 0004: Spotify playback and linking

- **Status:** accepted
- **Date:** 2026-10-05
- **Issue:** MAD-697

## Context

Syncphony plays every song through one speaker phone, from the account of the friend who queued it (ADR 0001, ADR 0003). Spotify offers two official ways to play: the Web Playback SDK, which doesn't run in mobile browsers, and Spotify Connect, which only targets devices logged into the *same* account. Neither lets one phone play songs from several friends' accounts.

Spotify's own apps get audio over a streaming protocol: log into an "access point", ask for a track's decryption key, download the encrypted file from a CDN, and decrypt it with AES-CTR. [go-librespot](https://github.com/devgianlu/go-librespot) (GPL-3.0) implements it. The spike for this issue (not shipped) established:

- **Our developer app can't log into the streaming protocol.** The access point accepts tokens only from Spotify's own client IDs. Logging in with go-librespot's (Spotify's desktop client ID) through a device pairing works: the user approves a code at `spotify.com/pair` on any device, phones included. It yields reusable credentials, so a friend pairs once.
- **That token is no use for the Web API.** The public Web API rate-limited it on the first search.
- **Users couldn't sign into our developer app.** Spotify's `/authorize` answered `server_error` after every login, for every account we tried, with any scopes, while the app's own token (the client credentials grant) worked. Spotify's developer forum reports the same for other development-mode apps.
- **Decryption is seekable, and the files are Ogg Vorbis** with a 167-byte Spotify header in front. Strip the header and it's a normal Ogg file, so the server can proxy it like Navidrome audio, with range requests intact.
- **Spotify refuses some tracks' keys.** About 30% of a sample of well-known tracks, at every bitrate, every time (key error code 1). The refusal is per track and account. go-librespot has the same limit. Getting around it means defeating Spotify's key obfuscation ("PlayPlay"), which is copy protection.
- **Spotify throttles fast key requests** (code 2). One key every few minutes, a party's pace, is fine.
- Everything we need builds with `CGO_ENABLED=0`. go-librespot's `session` package needs cgo for its decoders, so we use the lower-level packages and pass the Ogg through undecoded.

## Decision

**Stream Spotify through the server.** The provider is a `PlaybackStream` provider. Its `Audio` backend (`provider/spotify/streaming`) logs into the access point with the queuing friend's stored credentials, fetches the track's Ogg Vorbis file (the best one within the player's bitrate cap), decrypts it on the fly and strips the header. One connection per account is opened on first use and closed after 15 idle minutes.

**Link by device pairing alone.** Linking is OAuth2's device code flow: the user approves a code at spotify.com/pair, and the pairing's streaming credentials are what's stored. Users never sign into our developer app. Search, metadata and artwork come from the Web API with the app's own token, shared by every session and refreshed as it expires; if Spotify refuses it, that's reported as an outage, not as the user's link expiring. To support this, the provider interface gains device pairing (ADR 0001, "One linking API"):

- A new link method, `device`, and an optional `DevicePairer` interface (`BeginPairing`, `PollPairing`), modelled on RFC 8628. A `device` linker pairs as its only step. An `oauth2` linker may also implement `DevicePairer` to pair before its redirect, with the pairing's result in `LinkInput.Paired`; Spotify first linked that way, and it's kept for a provider that needs both.
- `links.Service` keeps pending pairings server-side, like OAuth2 states, and asks the service at most once per polling interval. The API adds `POST /pairings` and `GET /pairings/{id}`, and `POST /links/oauth` takes an approved `pairingId`. `ProviderInfo.pairing` tells the web app to show the code.

**List playlists through the streaming login.** The Web API only lists a user's playlists, and their tracks, to that user signed into the app. The paired login can read them from Spotify's own playlist service instead, as the Spotify apps do: the library ("rootlist") in one request, and a playlist a page at a time, with each page's track metadata from the extended metadata service in one more request. A `Library` backend (`provider/spotify/streaming`) does this, and Spotify sessions implement `PlaylistLister`. The API gains `GET /links/{id}/playlists` and `GET /links/{id}/playlists/{playlistId}/tracks`, and the search screen shows each linked service's playlists before you search. Playlists the user made without a cover picture have no artwork here: Spotify draws their mosaic of album covers on the fly, so the playlist page shows the first song's cover instead.

**List them as the Spotify apps' home does: Liked Songs and recently played first.** The recently played service gives the last 50 things the account played, with times. Playlists are sorted by those times, newest first; the ones never played follow in the library's order. Playlists played but not saved (Spotify's mixes, mostly; up to 20) are looked up one by one and listed too. Liked Songs comes from the collection service, which keeps saved albums and songs in one set in URI order, so the whole set is read (2,000 items a request) and sorted newest first. It's listed under the ID `liked`, and placed by when Spotify last played it, which it reports as a playlist of its own (format `liked-songs`), not shown twice; never played, it goes first, as Spotify pins it. The library lists some of Spotify's mixes with no length whether they have songs or not, so those are looked up too, and ones that are really empty (the DJ) are left out. Listing playlists now takes a few more requests (the library, recently played, the collection, and the lookups, four at a time), all within the 30-second bound.

**Refuse unplayable songs when they're added.** A new optional session interface, `PlayChecker`, lets the queue ask before adding a single song. Spotify's asks for the key, which the backend caches (a day, or an hour for a refusal), so playing the song later costs no second request. A refused song is rejected with `not_playable` and a message naming the song. Adds of several songs (an album) aren't checked, because a key request per song would trip the throttle; a refused song among them is skipped with a notice when its turn comes, as any song that fails to play is. We don't work around the refusals.

**Ship ffmpeg.** iOS Safari can't decode Ogg Vorbis, and the speaker can be any phone. The production image moves from distroless to Alpine with ffmpeg, keeping the same non-root UID. Transcoded streams start from the beginning and can't seek; Android and desktop browsers play the Ogg directly and can.

## Consequences

- Linking Spotify is one approval, from any device. With no user signing into the app, development mode's 5-user cap doesn't limit who can link.
- Each friend sees their own Liked Songs and playlists, most recently played first, with the mixes they played lately even if they never saved them. Recently played albums and artists aren't listed.
- Search results aren't tailored to each user's market, since the app's token has no user.
- The streaming protocol is unofficial. Spotify can change it, and go-librespot then needs an update before we do. The rest of the app keeps working; Spotify songs skip until then.
- Some songs can't be queued from Spotify. Phase 4's cross-service matching can fall back to the same song on Navidrome.
- go-librespot is GPL-3.0, which may be combined with Syncphony's AGPL-3.0 (GPL-3.0 section 13). One adapted file, `streaming/clienttoken.go`, stays under GPL-3.0 and says so.
- The image is larger (ffmpeg), and gains a shell and package manager it didn't have before.
