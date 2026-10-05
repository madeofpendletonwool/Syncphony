# Syncphony

One shared music queue for a group of friends, across everyone's streaming services.

Each friend links their own service (self-hosted Navidrome, Spotify, and more later). Everyone searches and adds songs. Syncphony plays them in **fair turns** from whichever service each song came from, through one player: usually a phone running the web app, connected to a Bluetooth speaker.

> **Status:** early. Phase 0 (foundation) is in place. See the roadmap below.

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
  cmd/syncphony/        entrypoint
  internal/config/      SYNCPHONY_* env config
  internal/httpapi/     API handlers (+ api.gen.go, generated)
  internal/store/       SQLite: goose migrations, sqlc queries (+ *.gen.go, generated)
  internal/provider/    provider interface, canonical types, registry
    fake/               in-memory provider for tests and UI development
    providertest/       conformance suite every provider runs
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

Images are published to `ghcr.io/madeofpendletonwool/syncphony`: `:main` tracks the main branch, and `v*` tags publish `:X.Y.Z` and `:latest`. Pick one with `SYNCPHONY_TAG` (e.g. `SYNCPHONY_TAG=main`).

## Roadmap

0. **Foundation:** repo, tooling, CI, image ✅
1. **Core platform:** data model, invite-only accounts, credential vault, provider interface, realtime
2. **Navidrome MVP:** Navidrome provider, fair queue, playback engine, web app and phone player
3. **Spotify provider**
4. **Party features:** vote-skip, fairness policies, history, cross-service track matching
5. **Native app and beyond:** Capacitor app, more providers, multiple rooms

## License

[AGPL-3.0](LICENSE). If you run a modified Syncphony as a service for others, you must share your changes.
