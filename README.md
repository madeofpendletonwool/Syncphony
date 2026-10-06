# Syncphony

One shared music queue for a group of friends, across everyone's streaming services.

Each friend links their own service (self-hosted Navidrome, Spotify, and more later). Everyone searches and adds songs. Syncphony plays them in **fair turns** from whichever service each song came from, through one player: usually a phone running the web app, connected to a Bluetooth speaker.

> **Status:** early. Phases 0–2 are in place: you can host a hangout with Navidrome and a phone on a Bluetooth speaker. See the roadmap below.

## How it fits together

```
 friends' phones ──REST/WebSocket──▶  Syncphony server (Go)  ◀── provider interface ──▶  Navidrome
 (search, queue)                      queue · fairness · auth                         ──▶  Spotify
                                      credential vault · audio proxy                  ──▶  (future providers)
 player phone ──<audio> stream──────▶                    │
 └─ Bluetooth speaker                                    SQLite
```

- **Providers** are plugins behind one Go interface. Each declares whether it **streams** audio through the server (Navidrome) or is driven as a **remote** player. Adding a service means adding a package, not touching the queue or the API.
- **Service credentials never leave the server.** They're encrypted at rest, and the server proxies audio to the player.

## Stack

| | |
|---|---|
| Server | Go (stdlib `net/http`), SQLite, WebSockets |
| API | `api/openapi.yaml` is the source of truth. Go server types (oapi-codegen) and TS client types (openapi-typescript) are generated from it. |
| Web | React 19, TypeScript, Vite, TanStack Router + Query, Tailwind v4, shadcn/ui, Motion |
| Ship | One Docker image: the Go binary with the built web app embedded |

## Repo layout

```
api/openapi.yaml        HTTP API contract
server/                 Go module
  cmd/syncphony/        entrypoint, and the `syncphony vault` commands
  internal/config/      SYNCPHONY_* env config
  internal/httpapi/     API handlers and the room WebSocket (+ api.gen.go, generated)
  internal/auth/        accounts: invites, passkeys, passwords, sessions
  internal/vault/       envelope encryption for linked-service credentials
  internal/links/       linking service accounts, link health
  internal/realtime/    event bus and presence
  internal/rooms/       room snapshots and change announcements
  internal/store/       SQLite: goose migrations, sqlc queries (+ *.gen.go, generated)
  internal/provider/    provider interface, canonical types, registry
    fake/               in-memory provider for tests and UI development
    providertest/       conformance suite every provider runs
    navidrome/          Navidrome (Subsonic API)
    spotify/            Spotify: Web API, linking
      streaming/        Spotify's streaming protocol, via go-librespot
  internal/transcode/   ffmpeg fallback for formats the player can't decode
  internal/webui/       embedded web app (production builds)
web/                    React app
  src/routes/           file-based routes (TanStack Router)
  src/api/              typed API client (+ schema.gen.ts, generated)
  src/components/ui/    shadcn/ui components
docs/adr/               architecture decision records
deploy/
  Dockerfile            production image
  compose.yml           example deployment behind Caddy (HTTPS)
  dev/                  local Navidrome + synthetic sample library
```

## Development

Requirements: Go 1.27+, Node 24+ with pnpm (`corepack enable`), Docker, and ffmpeg (optional; used to generate the sample library, falls back to Docker).

```sh
make setup   # install dependencies
make dev     # Navidrome + Go server (live reload) + Vite dev server
```

- Web app: http://localhost:5173 (proxies `/api` and `/ws` to the Go server)
- API: http://localhost:8080/api/healthz (if 8080 is taken: `make dev DEV_API_PORT=8099`)
- Dev Navidrome: http://localhost:4533, user `admin`, password `syncphony`. It's preloaded with a small synthetic library, so you don't need real music or accounts.

Other tasks: `make gen` (after editing `api/openapi.yaml`, or the SQL in `server/internal/store`), `make test`, `make lint`, `make build` (single binary at `server/bin/syncphony`), `make docker`. Run `make help` for the full list.

Configuration is via `SYNCPHONY_*` environment variables. See [`.env.example`](.env.example).

## Deployment

```sh
SYNCPHONY_BASE_URL=https://syncphony.example.com docker compose -f deploy/compose.yml up -d
```

This serves on port 8080, ready for your existing reverse proxy. If you don't have one, add `--profile caddy` and Caddy handles HTTPS for the host in `SYNCPHONY_BASE_URL`.

**First run:** there are no accounts yet, so the server logs a one-time setup link (`docker compose logs syncphony`). Whoever opens it becomes the admin. The link lasts 24 hours, and restarting while there are still no accounts prints a new one. After that, Syncphony is invite-only: admins create invite links for friends. Everyone can sign in with a passkey, a password, or both.

**Credential vault:** linked-service credentials are encrypted at rest with a master key. If you don't set one, the server generates `vault.key` in the data directory on first run. That's convenient, but a backup of the data directory then holds both the key and the credentials it protects. For better protection, set `SYNCPHONY_VAULT_KEY` (or `SYNCPHONY_VAULT_KEY_FILE`, e.g. a Docker secret) and keep the key somewhere else. To rotate the key, run `syncphony vault` for the steps (`docker compose exec syncphony syncphony vault rotate`). Losing the key means everyone links their services again; nothing else is lost.

**Spotify:** to offer Spotify, register an app at [developer.spotify.com](https://developer.spotify.com/dashboard). The account that creates it must have Premium.

1. Spotify asks for a redirect URI; `<SYNCPHONY_BASE_URL>/api/links/oauth/callback` will do. Nobody signs into the app, so it isn't used.
2. Under **APIs used**, tick **Web API**.
3. Put the app's client ID and secret in `SYNCPHONY_SPOTIFY_CLIENT_ID` and `SYNCPHONY_SPOTIFY_CLIENT_SECRET` (in `.env` next to `compose.yml`), and restart.

Friends link Spotify by approving a code at spotify.com/pair, from any device. The server searches with the app's own token and streams through each friend's account, so every linked account needs Premium. Your Liked Songs and Spotify playlists, most recently played first, show on the Search screen before you search. Spotify won't let Syncphony play some songs; those are refused when you add them. [ADR 0004](docs/adr/0004-spotify-playback.md) explains why.

Images are published to `ghcr.io/madeofpendletonwool/syncphony`: `:main` tracks the main branch, and `v*` tags publish `:X.Y.Z` and `:latest`. Pick one with `SYNCPHONY_TAG` (e.g. `SYNCPHONY_TAG=main`).

## Playing at a hangout

1. Pair a phone with the Bluetooth speaker and open Syncphony on it. On iPhone, add it to the Home Screen first (Share → Add to Home Screen).
2. On the Room screen, tap **Play on this phone**. That phone is now the speaker: it plays the queue, and its lock screen and the speaker's buttons control the room.
3. Everyone else opens Syncphony on their own phone, searches, and adds songs to their lane.

What works where (and what doesn't yet) is in [docs/player-mode.md](docs/player-mode.md).

## Roadmap

0. **Foundation:** repo, tooling, CI, image ✅
1. **Core platform:** data model, invite-only accounts, credential vault, provider interface, realtime ✅
2. **Navidrome MVP:** Navidrome provider, fair queue, playback engine, web app and phone player ✅
3. **Spotify provider**
4. **Party features:** vote-skip, fairness policies, history, cross-service track matching
5. **Native app and beyond:** Capacitor app, more providers, multiple rooms

## License

[AGPL-3.0](LICENSE). If you run a modified Syncphony as a service for others, you must share your changes. Spotify streaming uses [go-librespot](https://github.com/devgianlu/go-librespot) (GPL-3.0); `server/internal/provider/spotify/streaming/clienttoken.go` is adapted from it and stays under GPL-3.0.
