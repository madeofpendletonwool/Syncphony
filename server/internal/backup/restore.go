// SPDX-License-Identifier: AGPL-3.0-only

package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// A restore is staged as a checked copy of the backup in the data
// directory, and applied when the server next starts, before it opens the
// database: the database can't be swapped under a running server.
const (
	stagedDB   = "restore.db"
	stagedInfo = "restore.json"
	// DBFile is the database's file name in the data directory.
	DBFile = "syncphony.db"
)

// Pending is a restore waiting for the server to restart.
type Pending struct {
	// From is the backup's name, or the file's for one from elsewhere.
	From     string    `json:"from"`
	StagedAt time.Time `json:"stagedAt"`
	store.Snapshot
}

// Verify checks the backup file at path all the way through, and that
// this version of Syncphony can run on it.
func Verify(ctx context.Context, path string) (store.Snapshot, error) {
	snap, err := store.Inspect(ctx, path, true)
	if err != nil {
		return snap, err
	}
	if snap.SchemaVersion > store.SchemaVersion() {
		return snap, &InvalidInputError{fmt.Sprintf("this backup is from a newer Syncphony (schema %d; this one knows up to %d): upgrade first", snap.SchemaVersion, store.SchemaVersion())}
	}
	return snap, nil
}

// Stage restores the backup called name, or the file at that path, the
// next time the server starts. It replaces any restore already staged.
func (s *Service) Stage(ctx context.Context, nameOrPath string) (Pending, error) {
	path, err := s.Path(nameOrPath)
	if errors.Is(err, ErrNotFound) && filepath.Base(nameOrPath) != nameOrPath {
		path, err = nameOrPath, nil
	}
	if err != nil {
		return Pending{}, err
	}
	snap, err := Verify(ctx, path)
	if err != nil {
		return Pending{}, err
	}
	if err := s.Cancel(); err != nil {
		return Pending{}, err
	}
	// A copy, so the rotation can't delete it before the restart.
	dst := filepath.Join(s.DataDir, stagedDB)
	if err := copyFile(path, dst+partialExt); err != nil {
		return Pending{}, err
	}
	if err := os.Rename(dst+partialExt, dst); err != nil {
		return Pending{}, err
	}
	p := Pending{From: filepath.Base(path), StagedAt: s.Now().UTC(), Snapshot: snap}
	raw, err := json.Marshal(p)
	if err != nil {
		return Pending{}, err
	}
	if err := os.WriteFile(filepath.Join(s.DataDir, stagedInfo), raw, 0o600); err != nil {
		return Pending{}, errors.Join(err, s.Cancel())
	}
	return p, nil
}

// Staged returns the restore waiting for a restart, or nil.
func (s *Service) Staged() (*Pending, error) {
	if _, err := os.Stat(filepath.Join(s.DataDir, stagedDB)); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	p := &Pending{From: stagedDB}
	if raw, err := os.ReadFile(filepath.Join(s.DataDir, stagedInfo)); err == nil {
		_ = json.Unmarshal(raw, p)
	}
	return p, nil
}

// Cancel drops the restore waiting for a restart, if any.
func (s *Service) Cancel() error {
	var errs []error
	for _, f := range []string{stagedDB, stagedDB + partialExt, stagedInfo} {
		if err := os.Remove(filepath.Join(s.DataDir, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ApplyStaged restores the staged backup, if there is one: it backs up
// the database it replaces (as PreRestore), then puts the backup in its
// place. Call it at startup, before opening the database.
func (s *Service) ApplyStaged(ctx context.Context) (*Pending, error) {
	p, err := s.Staged()
	if err != nil || p == nil {
		return nil, err
	}
	staged := filepath.Join(s.DataDir, stagedDB)
	if _, err := store.Inspect(ctx, staged, false); err != nil {
		return nil, fmt.Errorf("restoring %s: %w (delete %s to start without restoring)", p.From, err, staged)
	}
	db := filepath.Join(s.DataDir, DBFile)
	if _, err := os.Stat(db); err == nil {
		b, err := s.backupFile(ctx, db, PreRestore)
		if err != nil {
			return nil, fmt.Errorf("restoring %s: backing up the database it replaces: %w", p.From, err)
		}
		slog.Info("backed up the database before restoring", "file", b.Name)
	}
	for _, f := range []string{db + "-wal", db + "-shm", db} {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("restoring %s: %w", p.From, err)
		}
	}
	if err := os.Rename(staged, db); err != nil {
		return nil, fmt.Errorf("restoring %s: %w", p.From, err)
	}
	_ = os.Remove(filepath.Join(s.DataDir, stagedInfo))
	return p, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // a backup the admin chose, checked by Verify
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // in the data directory
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return errors.Join(err, os.Remove(dst))
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
