// SPDX-License-Identifier: AGPL-3.0-only

package admin_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/admin"
	"github.com/madeofpendletonwool/syncphony/server/internal/backup"
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
	db, _ := open(t)
	s := admin.New(db)
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

func TestBackupSchedule(t *testing.T) {
	db, _ := open(t)
	s := admin.New(db)
	if st, err := s.Settings(t.Context()); err != nil || st.Backups != backup.DefaultSchedule {
		t.Fatalf("default schedule: %+v, %v", st.Backups, err)
	}
	// Midnight and keeping none are real choices, not "use the default".
	sch := backup.Schedule{Frequency: backup.Weekly, Hour: 0, Weekday: time.Friday, KeepDaily: 0, KeepWeekly: 8, KeepMonthly: 12}
	if _, err := s.Update(t.Context(), admin.Update{Backups: &sch}); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Settings(t.Context()); st.Backups != sch {
		t.Errorf("saved schedule: %+v", st.Backups)
	}
	var invalid *backup.InvalidInputError
	if _, err := s.Update(t.Context(), admin.Update{Backups: &backup.Schedule{Frequency: "hourly"}}); !errors.As(err, &invalid) {
		t.Errorf("bad schedule: %v", err)
	}
}
