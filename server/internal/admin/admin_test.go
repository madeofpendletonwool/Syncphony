// SPDX-License-Identifier: AGPL-3.0-only

package admin_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/admin"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

func open(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "syncphony.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, dir
}

func TestSettings(t *testing.T) {
	db, dir := open(t)
	s := admin.New(db, filepath.Join(dir, "backups"))
	st, err := s.Settings(t.Context())
	if err != nil || st.InstanceName != "" || st.InviteExpiry() != 7*24*time.Hour {
		t.Fatalf("defaults: %+v, %v", st, err)
	}
	st, err = s.Update(t.Context(), admin.Update{InstanceName: new("  The Den "), InviteExpiryHours: new(48)})
	if err != nil || st.InstanceName != "The Den" || st.InviteExpiry() != 48*time.Hour {
		t.Fatalf("updated: %+v, %v", st, err)
	}
	// Only what's set changes.
	if st, err = s.Update(t.Context(), admin.Update{InviteExpiryHours: new(24)}); err != nil || st.InstanceName != "The Den" {
		t.Fatalf("partial: %+v, %v", st, err)
	}
	var invalid *admin.InvalidInputError
	for _, u := range []admin.Update{
		{InviteExpiryHours: new(0)},
		{InviteExpiryHours: new(721)},
		{InstanceName: new("a name that is far too long for anyone's server")},
	} {
		if _, err := s.Update(t.Context(), u); !errors.As(err, &invalid) {
			t.Errorf("%+v: %v", u, err)
		}
	}
	if st, _ := s.Settings(t.Context()); st.InstanceName != "The Den" || st.InviteExpiryHours != 24 {
		t.Errorf("after bad updates: %+v", st)
	}
}

func TestBackups(t *testing.T) {
	db, dir := open(t)
	s := admin.New(db, filepath.Join(dir, "backups"))
	if bs, err := s.Backups(); err != nil || len(bs) != 0 {
		t.Fatalf("no backups yet: %v, %v", bs, err)
	}
	now := time.Date(2026, 10, 6, 19, 30, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	first, err := s.Backup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != "syncphony-20261006-193000.db" || first.Bytes == 0 {
		t.Fatalf("backup: %+v", first)
	}
	// The copy is a working database.
	copied, err := store.Open(t.Context(), filepath.Join(dir, "backups", first.Name))
	if err != nil {
		t.Fatal(err)
	}
	copied.Close()
	if _, err := s.Backup(t.Context()); err == nil {
		t.Error("two backups in one second")
	}
	// Only the newest are kept.
	for range admin.KeepBackups + 2 {
		now = now.Add(time.Hour)
		if _, err := s.Backup(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	bs, err := s.Backups()
	if err != nil || len(bs) != admin.KeepBackups || !bs[0].CreatedAt.Equal(now) {
		t.Fatalf("kept %d: %+v, %v", len(bs), bs, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "backups", first.Name)); !os.IsNotExist(err) {
		t.Errorf("oldest backup still there: %v", err)
	}
}
