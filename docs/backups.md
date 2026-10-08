# Backups

Syncphony keeps everything in one SQLite database: accounts, rooms, queues, history, settings, and linked services' credentials. It backs that database up on a schedule into a folder you choose. Everything else in the data folder is a cache Syncphony rebuilds by itself. [ADR 0014](adr/0014-backups.md) explains the design.

Admins manage backups on **Settings → Server → Backups**. There you can see the folder and when the next backup is due, change the schedule and how many are kept, back up now, and download, restore or delete a backup. The `syncphony backup` command does the same from a shell.

## Where backups go

Backups go in `SYNCPHONY_BACKUP_DIR`. The container image sets it to `/backups`, and `deploy/compose.yml` mounts a volume there. To keep backups on another disk or a NAS, which is the point of having them, mount a host folder at `/backups` instead:

```sh
# The container runs as UID 65532, which must be able to write there.
sudo mkdir -p /mnt/nas/syncphony-backups
sudo chown 65532:65532 /mnt/nas/syncphony-backups

# In .env next to compose.yml:
SYNCPHONY_BACKUP_PATH=/mnt/nas/syncphony-backups
```

Or edit the volume line in `compose.yml` directly:

```yaml
    volumes:
      - syncphony-data:/data
      - /mnt/nas/syncphony-backups:/backups
```

If the folder can't be written, the server logs why at startup, and the Server page shows it too.

Outside Docker, `SYNCPHONY_BACKUP_DIR` defaults to `<data dir>/backups`.

## When backups are made

By default, a backup is made every night at 3:00, server time. On the Server page you can choose every 6 hours, every 12 hours, daily, weekly or off, and the hour. Every 6 or 12 hours counts from that hour, so 3:00 every 6 hours means 3:00, 9:00, 15:00 and 21:00. The hours are in the server's time zone: set `TZ` (for example `TZ=Europe/London` in `.env`) so they're yours. Without it, a container runs in UTC.

If the server was down when a backup was due, it makes one as soon as it starts. A failed backup is logged, shown on the Server page, and tried again 15 minutes later.

Syncphony also makes a backup:

- **by hand**, from **Back up now** or `syncphony backup now`;
- **before an upgrade**, when a new version is about to change the database. If the new version has a problem, you can go back to the old one with this backup;
- **before a restore**, of the database the restore replaces.

Each backup is written under a temporary name, checked with SQLite's `quick_check`, and only then renamed into place. A file named `syncphony-*.db` in the folder is always a complete, checked backup.

## What's kept

Scheduled backups are rotated:

- every backup from the last 24 hours;
- the newest backup of each of the last **7 days**, **4 weeks** and **6 months**. You can change these counts on the Server page: up to 90 days, 52 weeks and 36 months, or 0;
- always the newest backup.

Backups made by hand are kept apart from the rotation: the newest 10. Backups from before upgrades and restores are kept 3 of each. Backups are pruned after each new one, and you can delete any of them yourself.

File names say when each was made (in UTC) and why: `syncphony-20261006-030000-scheduled.db`, `-manual`, `-pre-upgrade`, `-pre-restore`.

## The vault key

Linked services' passwords and tokens are encrypted in the database, and stay encrypted in backups. **Restoring a backup needs the vault key they were sealed with.** Without the key, everything else restores, but everyone has to link their services again.

So back up the key too, but **not next to the backups**: whoever has both can read the credentials. Syncphony never writes the key into the backup folder, and warns at startup if `SYNCPHONY_VAULT_KEY_FILE` points inside it.

- If you set `SYNCPHONY_VAULT_KEY` or `SYNCPHONY_VAULT_KEY_FILE`, keep a copy of that value in your password manager.
- If you didn't, the key is `vault.key` in the data folder (`docker compose exec syncphony cat /data/vault.key`). Copy it to your password manager. Better still, set it as `SYNCPHONY_VAULT_KEY` from now on.

The Server page shows the current key's ID. `syncphony backup verify` says whether this server's key opens a backup.

## Restoring

A restore is staged while the server runs, and applied when it next starts. The database can't be swapped out from under a running server. When it starts, the server:

1. checks the staged backup again;
2. backs up the current database as a `pre-restore` backup, so a restore can be undone;
3. puts the backup in its place;
4. migrates it, if it's from an older version. A `pre-upgrade` backup is made first.

### From the Server page

1. Under **Backups**, tap the restore icon on the backup, then **Restore on restart?** to confirm. It's checked all the way through first. If its linked services need a vault key the server doesn't have, you're told.
2. Restart the server: `docker compose restart syncphony`.

Changed your mind? Tap **Don't restore** before restarting.

### From the command line

```sh
docker compose exec syncphony syncphony backup list
docker compose exec syncphony syncphony backup verify syncphony-20261006-030000-scheduled.db
docker compose exec syncphony syncphony backup restore syncphony-20261006-030000-scheduled.db
docker compose restart syncphony
```

If the server won't start, use `run` instead of `exec`, which starts a one-off container with the same volumes:

```sh
docker compose run --rm syncphony backup restore syncphony-20261006-030000-scheduled.db
docker compose up -d
```

To restore a file from elsewhere, like a downloaded backup or one from another server, put it somewhere the container can see (the backup folder is easiest) and pass its path:

```sh
docker compose cp ./syncphony-old.db syncphony:/backups/
docker compose exec syncphony syncphony backup restore /backups/syncphony-old.db
```

`syncphony backup cancel-restore` drops a staged restore.

### Onto a new machine

1. Install Syncphony as usual, with the same `SYNCPHONY_VAULT_KEY` (or key file) as the old server, and the backup folder mounted.
2. `docker compose run --rm syncphony backup restore /backups/<backup>`
3. `docker compose up -d`

A backup from a newer version of Syncphony than the one restoring it is refused. Upgrade first.

### By hand

A backup is a plain SQLite database. With the server stopped, you can also copy it over `syncphony.db` in the data folder, after deleting `syncphony.db-wal` and `syncphony.db-shm`. Doing it through `syncphony backup restore` gives you the checks and the `pre-restore` backup.

## Command reference

```text
syncphony backup now              back up now (safe while the server runs)
syncphony backup list             list backups, newest first
syncphony backup verify <backup>  check one all the way through; show what's in it and whether the vault key opens it
syncphony backup restore <backup> restore one when the server next starts
syncphony backup cancel-restore   don't
```

`<backup>` is a name from `list` or a path to a file. `syncphony admin backup` still works and is the same as `syncphony backup now`.

The restore procedure is tested in CI: `server/internal/backup`'s tests stage and restore backups made by this version and by the oldest schema, then run migrations on them.
