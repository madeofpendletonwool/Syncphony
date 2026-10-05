// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrationsReversible runs every migration down and up again.
func TestMigrationsReversible(t *testing.T) {
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, s.db, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(t.Context(), 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := p.Up(t.Context()); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
