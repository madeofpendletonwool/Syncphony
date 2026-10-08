// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/madeofpendletonwool/syncphony/server/internal/admin"
	"github.com/madeofpendletonwool/syncphony/server/internal/backup"
	"github.com/madeofpendletonwool/syncphony/server/internal/config"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/vault"
)

const backupUsage = `usage: syncphony backup <command>

commands:
  now              back up the database into the backup folder. Safe while
                   the server runs.
  list             list the backups, newest first.
  verify <backup>  check a backup all the way through, and say what's in it
                   and whether this server's vault key opens it.
  restore <backup> restore a backup the next time the server starts: it's
                   checked and set aside now, and the database it replaces
                   is backed up first. Restart the server to finish.
  cancel-restore   don't restore after all.

<backup> is a backup's name from "list", or the path to a backup file.
Backups go in SYNCPHONY_BACKUP_DIR (default <data dir>/backups), and are
made on a schedule set on the Server page. See docs/backups.md.

Run it where the server's SYNCPHONY_* settings are set, e.g.
  docker compose exec syncphony syncphony backup list`

func backupCommand(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, backupUsage)
		return errors.New("backup: expected a command")
	}
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	dir, err := filepath.Abs(cfg.BackupDir)
	if err != nil {
		return err
	}
	// Not openBackups or setup: those would apply a staged restore, under
	// the running server.
	svc := backup.New(dir, cfg.DataDir)
	arg := func() (string, error) {
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, backupUsage)
			return "", fmt.Errorf("backup %s: expected one backup", args[0])
		}
		return args[1], nil
	}
	out := os.Stdout
	switch args[0] {
	case "now":
		path := filepath.Join(cfg.DataDir, backup.DBFile)
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("backup now: no database at %s (is SYNCPHONY_DATA_DIR right?)", path)
		}
		db, err := store.Open(ctx, path)
		if err != nil {
			return err
		}
		defer db.Close()
		svc.DB = db
		svc.Schedule = admin.New(db).BackupSchedule
		b, err := svc.Backup(ctx, backup.Manual)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Backed up to %s (%s).\n", filepath.Join(dir, b.Name), size(b.Bytes))
		fmt.Fprintln(out, "Linked services' credentials in it are sealed: restoring needs the same vault key.")
		return nil
	case "list":
		return listBackups(svc, out)
	case "verify":
		name, err := arg()
		if err != nil {
			return err
		}
		path, err := backupPath(svc, name)
		if err != nil {
			return err
		}
		snap, err := backup.Verify(ctx, path)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		fmt.Fprintf(out, "%s is OK.\n", path)
		describe(out, cfg, snap)
		return nil
	case "restore":
		name, err := arg()
		if err != nil {
			return err
		}
		if filepath.Base(name) != name {
			if name, err = filepath.Abs(name); err != nil {
				return err
			}
		}
		p, err := svc.Stage(ctx, name)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		fmt.Fprintf(out, "%s is checked and will be restored when the server next starts.\n", p.From)
		describe(out, cfg, p.Snapshot)
		fmt.Fprintln(out, "\nRestart the server to finish (e.g. docker compose restart syncphony).")
		fmt.Fprintln(out, "The database it replaces is backed up first. To change your mind: syncphony backup cancel-restore")
		return nil
	case "cancel-restore":
		p, err := svc.Staged()
		if err != nil {
			return err
		}
		if p == nil {
			fmt.Fprintln(out, "No restore is waiting.")
			return nil
		}
		if err := svc.Cancel(); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s won't be restored.\n", p.From)
		return nil
	default:
		fmt.Fprintln(os.Stderr, backupUsage)
		return fmt.Errorf("backup: unknown command %q", args[0])
	}
}

func backupPath(svc *backup.Service, name string) (string, error) {
	path, err := svc.Path(name)
	if errors.Is(err, backup.ErrNotFound) && filepath.Base(name) != name {
		return name, nil
	}
	return path, err
}

func listBackups(svc *backup.Service, out io.Writer) error {
	bs, err := svc.List()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Backups in %s:\n\n", svc.Dir)
	if len(bs) == 0 {
		fmt.Fprintln(out, "  none yet")
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, b := range bs {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", b.Name, b.CreatedAt.Local().Format("Mon 2 Jan 2006 15:04"), b.Kind, size(b.Bytes))
	}
	w.Flush()
	if p, err := svc.Staged(); err == nil && p != nil {
		fmt.Fprintf(out, "\n%s will be restored when the server next starts.\n", p.From)
	}
	return nil
}

// describe says what's in a backup, and whether this server can open its
// linked services' credentials.
func describe(out io.Writer, cfg config.Config, snap store.Snapshot) {
	fmt.Fprintf(out, "  schema version %d (this Syncphony: %d), %d user(s), %d linked service(s)\n", snap.SchemaVersion, store.SchemaVersion(), snap.Users, snap.Links)
	if len(snap.KeyIDs) == 0 {
		return
	}
	v, err := currentVault(cfg)
	if err != nil {
		fmt.Fprintf(out, "  sealed with vault key %s; couldn't load this server's: %v\n", strings.Join(snap.KeyIDs, ", "), err)
		return
	}
	var missing []string
	for _, id := range snap.KeyIDs {
		if !v.Has(id) {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		fmt.Fprintf(out, "  linked services are sealed with vault key %s, which this server has\n", strings.Join(snap.KeyIDs, ", "))
		return
	}
	fmt.Fprintf(out, "  WARNING: some linked services are sealed with vault key %s, which this server doesn't have (it has %s).\n", strings.Join(missing, ", "), v.CurrentKeyID())
	fmt.Fprintln(out, "  Set SYNCPHONY_VAULT_KEY (or SYNCPHONY_VAULT_OLD_KEYS) to it, or those services will need linking again.")
}

// currentVault loads the server's vault without making a key if it has none.
func currentVault(cfg config.Config) (*vault.Vault, error) {
	keyFile := filepath.Join(cfg.DataDir, "vault.key")
	if cfg.Vault.Key == "" && cfg.Vault.KeyFile == "" {
		if _, err := os.Stat(keyFile); err != nil {
			return nil, fmt.Errorf("no vault key at %s", keyFile)
		}
	}
	v, _, err := vault.Load(cfg.Vault, keyFile)
	return v, err
}

func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
