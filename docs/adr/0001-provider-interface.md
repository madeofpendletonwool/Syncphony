# ADR 0001: Provider interface

- **Status:** accepted
- **Date:** 2026-10-05
- **Issue:** MAD-683

## Context

Syncphony plays one shared queue from many music services. Navidrome and Spotify come first. Jellyfin, Plex, YouTube Music, Apple Music and local files may follow. These services differ in almost every way that matters:

- **How audio plays.** Navidrome hands us a file we can proxy to the player. Spotify Connect plays on Spotify's own device, and we can only tell it what to play.
- **How accounts link.** Navidrome uses a form (URL, username, password). Spotify uses an OAuth2 redirect with PKCE and refresh tokens.
- **What they can do.** Some have playlists, lyrics or ISRCs, and some don't.

If the queue, playback and API code handled these differences directly, every new service would touch all three. We want adding a service to mean adding one package.

## Decision

All services sit behind the interfaces in `server/internal/provider`. Nothing outside `internal/provider/<name>` and `main` imports a specific provider.

### Shape

```go
type Provider interface {
    Info() Info                                  // id, name, icon, Capabilities
    Linker() Linker                              // how a user connects an account
    Open(ctx, Link) (Session, error)             // client for one linked account
}

type Session interface {                         // required for every provider
    Search(ctx, SearchQuery) (SearchPage, error)
    Track(ctx, id) (Track, error)
    Album(ctx, id) (Album, []Track, error)
    Artist(ctx, id) (Artist, []Album, error)
    Artwork(ctx, ArtworkRef, size) (io.ReadCloser, string, error)
    Close() error
}

// Optional, found by type assertion and declared in Capabilities:
type Streamer interface { Stream(ctx, trackID, StreamOpts) (*AudioStream, error) }
type Remote interface   { Play; Pause; Resume; Seek; State }
type PlaylistLister interface { Playlists; PlaylistTracks }
type Lyricist interface { Lyrics(ctx, trackID) (Lyrics, error) }
```

The full definitions and doc comments are in the package. These are the main choices and why we made them.

**A small required core, with optional interfaces.** Every provider can search and look up tracks, so `Session` requires only that. (`Artist` was added with the Navidrome provider, MAD-689, so artist search results lead somewhere. Like `Artwork`, it returns `ErrUnsupported` when the provider doesn't declare the matching capability, here artist search.) Features that not every service has (streaming bytes, remote control, playlists, lyrics) are separate interfaces found by type assertion. This is the pattern `io.WriterTo` and `http.Flusher` use. We didn't use one large interface with `ErrUnsupported` stubs, because a missing method should be a compile-time fact and not a runtime surprise.

**Capabilities are declared as well as implemented.** The UI needs to know what a provider supports before it opens a session (for example, to hide a "Playlists" tab), so `Info().Capabilities` declares it. The fact that the two can disagree is the cost of this choice. The conformance suite checks that they agree.

**Two playback modes.** `PlaybackStream` providers return an `AudioStream` (body, content type, offset/length/size, seekable). The server proxies it to the player and handles HTTP range requests. `PlaybackRemote` providers expose transport controls and a polled `RemoteState`. The playback engine branches on the mode once, and the queue never needs to know.

**One linking API for forms and OAuth2.** A `Linker` declares its `Method`. Credential linkers describe their form with `Fields` (with kinds such as `FieldSecret`, so the UI masks them and logs skip them). OAuth2 linkers implement `BeginOAuth`. Both finish in `Complete(LinkInput) → (Credentials, AccountInfo)`. Compared with the first sketch:

- `BeginOAuth` takes the redirect URL. The core owns the callback route, so providers never need to know the server's base URL.
- `BeginOAuth` returns an opaque `Secret` (for example, a PKCE verifier). The core keeps it server-side next to the state token and passes it back to `Complete`. Linkers stay stateless, so a restart between redirect and callback doesn't break linking.

**Credentials are opaque bytes.** Only the provider that produced them can read them. The vault (MAD-686) encrypts and stores them without knowing their shape. When a session rotates credentials (for an OAuth2 refresh), it calls `Link.Sink`, and the vault re-encrypts and saves the new ones. Providers never touch storage.

**Canonical types.** A `TrackRef{Provider, LinkID, ID}` identifies a playable item anywhere in the system. `LinkID` matters because two friends may each link Spotify, and a track plays through the account of the person who queued it. `Track` holds normalized metadata. The queue stores the ref together with a metadata snapshot, so it still renders when a provider is offline. Album, artist and playlist IDs are plain strings that only the session that returned them understands. They are browsed and never queued, so they don't need a full ref.

**Typed errors.** Providers map service failures to `ErrNotFound`, `ErrAuthExpired`, `ErrInvalidCredentials`, `ErrUnavailable`, `ErrRateLimited` (with `RateLimitError.RetryAfter`), `ErrRange` and `ErrUnsupported`. The core reacts to each the same way for every service: `ErrAuthExpired` asks the user to relink, `ErrUnavailable` skips the track and retries later, and so on. `ErrInvalidCredentials` is kept apart from `ErrAuthExpired` because "your password is wrong" during linking and "your token was revoked" later lead to different UI.

**An explicit registry, not `init()` side effects.** `main` builds a `provider.Registry` from the providers it wants. Tests build their own registries. `Register` validates `Info` (ID format, known playback mode, a consistent linker), so a malformed provider fails at startup and not at the first request.

### Supporting pieces

- **`provider/fake`** is an in-memory provider with a fixed library of sine-tone WAV tracks (each at its own pitch, so you can hear which one is playing), SVG artwork, playlists and lyrics. It can act as a stream or remote provider, and link by form or by pretend OAuth2. Tests can `Revoke` an account, `Fail` every call (to simulate outages or rate limits), and drive a fake clock to exercise token refresh. It doubles as a backend for UI development before the real providers exist.
- **`provider/providertest`** is the conformance suite every provider runs with a small `Harness`. It checks that capabilities match the implemented interfaces, that search results resolve through `Track`/`Album`, that refs carry the right provider and link, that paging advances, that ranged streams return exactly the requested bytes, that remote transport state follows commands, that missing items return `ErrNotFound`, that revoked credentials return `ErrAuthExpired`, and that concurrent calls are race-free. It runs against `fake` in all four combinations of playback mode and link method.
- **`transcode`** is the server-level fallback for formats the player can't decode (for example, Ogg Vorbis on iOS Safari). `transcode.Stream` passes acceptable formats through untouched, with range support intact. Otherwise it restarts from byte 0 and pipes the audio through ffmpeg to MP3 or AAC. A transcoded stream isn't seekable. Providers that can pick a format themselves (Navidrome's `format=` parameter) should honor `StreamOpts.Accept`, so this fallback is rare.

## Consequences

- Adding a provider means writing `internal/provider/<name>`, passing `providertest.Run`, and adding one line in `main`.
- The core can't use a feature a service has unless the interface models it. That is deliberate. New optional interfaces can be added without breaking existing providers.
- Capabilities and interfaces can drift apart. The suite catches this, but only if every provider runs it in CI.
- Transcoding needs an ffmpeg binary. The production image is distroless and doesn't include one yet. When a client needs transcoding and ffmpeg is missing, the stream fails with `transcode.ErrNoTranscoder`. Shipping ffmpeg (or an image variant with it) is a follow-up decision.
- Remote providers report position by polling `State`. If a service offers push updates, its provider can poll faster internally. The interface doesn't need to change.
