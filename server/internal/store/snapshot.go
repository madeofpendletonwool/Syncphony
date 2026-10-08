// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Snapshot is what's in a copy of the database, read without opening it
// as the server's: Inspect never migrates or writes to it.
type Snapshot struct {
	// SchemaVersion is its newest applied migration.
	SchemaVersion int64
	// Users and Links count its accounts and linked services.
	Users, Links int
	// KeyIDs are the vault keys its linked services' credentials are
	// sealed with (vault.Key.ID), sorted. Restoring it needs them.
	KeyIDs []string
}

// ErrNotDatabase means a file isn't a Syncphony database.
var ErrNotDatabase = errors.New("not a Syncphony database")

// Inspect checks the database file at path and says what's in it. With
// full set, it runs SQLite's integrity_check, which reads every page;
// otherwise the faster quick_check.
func Inspect(ctx context.Context, path string, full bool) (Snapshot, error) {
	var snap Snapshot
	if _, err := os.Stat(path); err != nil {
		return snap, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return snap, err
	}
	defer db.Close()
	check := "quick_check"
	if full {
		check = "integrity_check"
	}
	rows, err := db.QueryContext(ctx, "PRAGMA "+check)
	if err != nil {
		// Not SQLite at all: "file is not a database".
		return snap, fmt.Errorf("%w: %w", ErrNotDatabase, err)
	}
	var problems []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			return snap, err
		}
		if line != "ok" {
			problems = append(problems, line)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return snap, fmt.Errorf("%w: %w", ErrNotDatabase, err)
	}
	if len(problems) > 0 {
		if len(problems) > 3 {
			problems = append(problems[:3], fmt.Sprintf("and %d more", len(problems)-3))
		}
		return snap, fmt.Errorf("the database is damaged: %s", strings.Join(problems, "; "))
	}
	err = db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied").Scan(&snap.SchemaVersion)
	if err != nil || snap.SchemaVersion == 0 {
		return snap, ErrNotDatabase
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&snap.Users); err != nil {
		return snap, fmt.Errorf("%w: %w", ErrNotDatabase, err)
	}
	keys, err := db.QueryContext(ctx, "SELECT substr(encrypted_credentials, 2, 8) AS key_id, COUNT(*) FROM service_links GROUP BY key_id ORDER BY key_id")
	if err != nil {
		return snap, fmt.Errorf("%w: %w", ErrNotDatabase, err)
	}
	defer keys.Close()
	for keys.Next() {
		var id []byte
		var n int
		if err := keys.Scan(&id, &n); err != nil {
			return snap, err
		}
		snap.Links += n
		snap.KeyIDs = append(snap.KeyIDs, hex.EncodeToString(id))
	}
	return snap, keys.Err()
}

// Copy writes a consistent copy of the database file at src to dst, which
// must not exist, without migrating it. Unlike BackupTo, src needn't be
// open: it's for copying a database the server isn't running on.
func Copy(ctx context.Context, src, dst string) error {
	db, err := sql.Open("sqlite", dsn(src))
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, "VACUUM INTO ?", dst)
	return err
}

// OpenVersion creates a database at path migrated only as far as version,
// as an older Syncphony would have left it. It's for testing that old
// backups restore.
func OpenVersion(ctx context.Context, path string, version int64) error {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return err
	}
	defer db.Close()
	p, err := migrator(db)
	if err != nil {
		return err
	}
	_, err = p.UpTo(ctx, version)
	return err
}
