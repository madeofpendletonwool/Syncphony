# ADR 0014: Backups

- **Status:** accepted
- **Date:** 2026-10-08
- **Issue:** MAD-729

## Context

Everything that matters is in one SQLite database. The Server page could back it up by hand into `<data dir>/backups`, keeping 7, and the docs suggested cron for anything regular. That kept the backups on the same volume as the database, made scheduling the admin's problem, and gave no way back other than copying files over by hand.

## Decision

### One file per backup, written with `VACUUM INTO`

A backup is a complete SQLite database, made with `VACUUM INTO` while the server runs. It's consistent and compact, and it includes what's still in the write-ahead log. It's written as `.syncphony-….db.partial`, checked with `quick_check`, then renamed into place. Anything named `syncphony-*.db` is a whole, checked backup, and a crash halfway through leaves only a `.partial` file, deleted at startup. Backups aren't compressed: a restore reads one with SQLite directly, and the database is small.

The name carries the time (UTC) and the reason: `syncphony-20261006-030000-scheduled.db`. Names from before this change, without a reason, count as made by hand. The folder is the only record. Listing reads names, so files copied in or deleted by hand are just as valid.

### A folder of its own

`SYNCPHONY_BACKUP_DIR` says where backups go, default `<data dir>/backups`. The image sets it to `/backups`, a separate volume, so `compose.yml` can put backups on another disk or a NAS with one line. The server checks it can write there at startup and on the Server page, and points at UID 65532 when it can't.

### The schedule is a setting, not configuration

When to back up and what to keep is in the server's settings (`server_settings`), changed on the Server page, not in environment variables: it's something an admin tunes, and seeing the next backup next to the controls helps. Frequencies are off, every 6 or 12 hours, daily and weekly, at an hour in the server's time zone (`TZ`).

`Run` checks every minute (and at once when the schedule changes) whether a slot has passed since the newest scheduled backup. Because of that, a backup missed while the server was down is made at startup, with no state beyond the files. A failure waits 15 minutes before the next try, so a full disk doesn't log every minute.

### Rotation: grandfather-father-son, and kinds apart

Scheduled backups keep everything from the last 24 hours, the newest of each of the last N days, weeks (ISO) and months, and always the newest. The defaults are 7, 4 and 6. Other kinds are kept apart, so a schedule can't push them out: 10 made by hand, and 3 each made before an upgrade or a restore. `Prune` is a pure function of the list, the schedule and the time.

### Backups before upgrades

`store.Open` takes `BeforeMigrate`. When an existing database has migrations to apply, the server backs it up first (`pre-upgrade`), so a bad upgrade can be undone by running the old version on that backup. If that backup fails, the server logs it and carries on: refusing to start over a full backup disk would be worse.

### Restores are staged, and applied at startup

A database can't safely be replaced under a running server, and `docker compose exec` runs inside one. So a restore, from the Server page or `syncphony backup restore`, is checked with `integrity_check`, refused if its schema is newer than this build's, and copied to `<data dir>/restore.db` with a note of where it came from. On the next start, before opening the database, the server backs up the current one (`pre-restore`), deletes it with its `-wal` and `-shm`, and renames the staged copy into place. Opening it then migrates it if it's from an older version. The CLI commands never apply a staged restore themselves: they'd be doing it under the server.

The server doesn't restart itself after staging. Not every install runs under a supervisor that brings it back, so the admin restarts it.

### The vault key

Backups hold linked services' credentials still sealed (`internal/vault`). Restoring needs the key, but the key must never sit next to the backups: whoever has both can read the credentials. Syncphony never writes it there, warns at startup if `SYNCPHONY_VAULT_KEY_FILE` is inside the backup folder, shows the current key's ID on the Server page, and reads the key IDs a backup's credentials are sealed with (`store.Inspect`) so a restore or `verify` can say whether this server can open them.

## Consequences

- Backups on the default volume are lost with the host. The docs and compose file push towards a separate mount, but can't make it so.
- The schedule's hour follows `TZ`. A container without it backs up at 3:00 UTC.
- Only the database is backed up. Artwork, palettes and beat maps are caches rebuilt on demand.
- Restoring takes a restart, and the admin has to do it.
- The restore procedure is tested in CI: `internal/backup`'s tests restore a backup of the current schema and one at the first migration, and migrate it.
