// SPDX-License-Identifier: AGPL-3.0-only

package backup_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/backup"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

type env struct {
	data string
	db   *store.Store
	svc  *backup.Service
	now  time.Time
}

func setup(t *testing.T) *env {
	t.Helper()
	e := &env{data: t.TempDir(), now: time.Date(2026, 10, 6, 19, 30, 0, 0, time.UTC)}
	e.svc = backup.New(filepath.Join(t.TempDir(), "backups"), e.data)
	e.svc.Now = func() time.Time { return e.now }
	e.svc.Location = time.UTC
	e.open(t)
	return e
}

func (e *env) open(t *testing.T, opts ...store.Option) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(e.data, backup.DBFile), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e.db, e.svc.DB = db, db
}

func addUser(t *testing.T, db *store.Store, name string) {
	t.Helper()
	_, err := db.CreateUser(t.Context(), store.CreateUserParams{
		ID: store.NewID(), Username: name, DisplayName: name, Color: "#fff", Role: store.RoleMember, CreatedAt: store.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func usernames(t *testing.T, db *store.Store) []string {
	t.Helper()
	us, err := db.ListUsers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, u := range us {
		out = append(out, u.Username)
	}
	slices.Sort(out)
	return out
}

func TestBackup(t *testing.T) {
	e := setup(t)
	if bs, err := e.svc.List(); err != nil || len(bs) != 0 {
		t.Fatalf("no backups yet: %v, %v", bs, err)
	}
	if err := e.svc.Check(); err != nil {
		t.Fatal(err)
	}
	b, err := e.svc.Backup(t.Context(), backup.Manual)
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "syncphony-20261006-193000-manual.db" || b.Kind != backup.Manual || b.Bytes == 0 {
		t.Fatalf("backup: %+v", b)
	}
	if _, err := e.svc.Backup(t.Context(), backup.Manual); err == nil {
		t.Error("two backups in one second")
	}
	// The copy is a working database, and nothing half-written is left.
	snap, err := backup.Verify(t.Context(), filepath.Join(e.svc.Dir, b.Name))
	if err != nil || snap.SchemaVersion != store.SchemaVersion() {
		t.Fatalf("verify: %+v, %v", snap, err)
	}
	entries, _ := os.ReadDir(e.svc.Dir)
	if len(entries) != 1 {
		t.Errorf("backup folder: %v", entries)
	}
	// Older names, without a kind, are listed as made on request.
	if err := os.WriteFile(filepath.Join(e.svc.Dir, "syncphony-20261001-120000.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.svc.Dir, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bs, err := e.svc.List()
	if err != nil || len(bs) != 2 || bs[1].Kind != backup.Manual {
		t.Fatalf("list: %+v, %v", bs, err)
	}
	if _, err := e.svc.Get("../syncphony.db"); !errors.Is(err, backup.ErrNotFound) {
		t.Errorf("a path isn't a backup's name: %v", err)
	}
	if err := e.svc.Delete(bs[1].Name); err != nil {
		t.Fatal(err)
	}
	if bs, _ := e.svc.List(); len(bs) != 1 {
		t.Errorf("after delete: %+v", bs)
	}
}

func TestCheckUnwritable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := t.TempDir()
	// Read-only, so the check can't write a file there.
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // a test's own folder
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // as above
	svc := backup.New(dir, t.TempDir())
	if err := svc.Check(); err == nil {
		t.Error("a read-only folder passed the check")
	}
}

func TestPrune(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	sch := backup.Schedule{Frequency: backup.Every6h, KeepDaily: 3, KeepWeekly: 2, KeepMonthly: 2}
	var all []backup.Backup
	// Every 6 hours for 90 days, plus some of each other kind.
	for at := now; at.After(now.AddDate(0, 0, -90)); at = at.Add(-6 * time.Hour) {
		all = append(all, backup.Backup{Name: at.String(), Kind: backup.Scheduled, CreatedAt: at})
	}
	for i := range 12 {
		at := now.Add(-time.Duration(i) * time.Hour).Add(-time.Minute)
		all = append(all, backup.Backup{Name: "m" + at.String(), Kind: backup.Manual, CreatedAt: at})
		all = append(all, backup.Backup{Name: "u" + at.String(), Kind: backup.PreUpgrade, CreatedAt: at})
	}
	slices.SortFunc(all, func(a, b backup.Backup) int { return b.CreatedAt.Compare(a.CreatedAt) })
	drop := backup.Prune(all, sch, now, time.UTC)
	var kept []backup.Backup
	for _, b := range all {
		if !slices.ContainsFunc(drop, func(d backup.Backup) bool { return d.Name == b.Name }) {
			kept = append(kept, b)
		}
	}
	count := map[backup.Kind]int{}
	var scheduled []time.Time
	for _, b := range kept {
		count[b.Kind]++
		if b.Kind == backup.Scheduled {
			scheduled = append(scheduled, b.CreatedAt)
		}
	}
	if count[backup.Manual] != 10 || count[backup.PreUpgrade] != 3 {
		t.Errorf("other kinds kept: %v", count)
	}
	want := []time.Time{
		// The last day: today's noon, 6am and midnight, and yesterday's 6pm.
		now, now.Add(-6 * time.Hour), now.Add(-12 * time.Hour), now.Add(-18 * time.Hour),
		// Then the newest of 3 days (today's and yesterday's already kept).
		time.Date(2026, 10, 4, 18, 0, 0, 0, time.UTC),
		// 2 weeks: this week's (today) and the one before, ending Sunday 4 Oct.
		// 2 months: October's (today) and September's newest.
		time.Date(2026, 9, 30, 18, 0, 0, 0, time.UTC),
	}
	if !slices.Equal(scheduled, want) {
		t.Errorf("scheduled kept:\n got %v\nwant %v", scheduled, want)
	}
}

func TestSchedule(t *testing.T) {
	// Tuesday afternoon.
	now := time.Date(2026, 10, 6, 15, 20, 0, 0, time.UTC)
	at := func(day, h int) time.Time { return time.Date(2026, 10, day, h, 0, 0, 0, time.UTC) }
	for _, c := range []struct {
		sch        backup.Schedule
		last, next time.Time
	}{
		{backup.Schedule{Frequency: backup.Daily, Hour: 3}, at(6, 3), at(7, 3)},
		{backup.Schedule{Frequency: backup.Daily, Hour: 18}, at(5, 18), at(6, 18)},
		{backup.Schedule{Frequency: backup.Every6h, Hour: 3}, at(6, 15), at(6, 21)},
		{backup.Schedule{Frequency: backup.Every12h, Hour: 20}, at(6, 8), at(6, 20)},
		{backup.Schedule{Frequency: backup.Weekly, Hour: 3, Weekday: time.Sunday}, at(4, 3), at(11, 3)},
		{backup.Schedule{Frequency: backup.Weekly, Hour: 16, Weekday: time.Tuesday}, at(29, 16).AddDate(0, -1, 0), at(6, 16)},
		{backup.Schedule{Frequency: backup.Off}, time.Time{}, time.Time{}},
	} {
		if got := c.sch.Last(now); !got.Equal(c.last) {
			t.Errorf("%+v: last %v, want %v", c.sch, got, c.last)
		}
		if got := c.sch.Next(now); !got.Equal(c.next) {
			t.Errorf("%+v: next %v, want %v", c.sch, got, c.next)
		}
	}
	for _, bad := range []backup.Schedule{
		{Frequency: "hourly"},
		{Frequency: backup.Daily, Hour: 24},
		{Frequency: backup.Daily, KeepDaily: -1},
		{Frequency: backup.Daily, KeepMonthly: backup.MaxKeepMonthly + 1},
	} {
		var invalid *backup.InvalidInputError
		if !errors.As(bad.Validate(), &invalid) {
			t.Errorf("%+v passed", bad)
		}
	}
	if err := backup.DefaultSchedule.Validate(); err != nil {
		t.Error(err)
	}
}

func TestScheduledRun(t *testing.T) {
	e := setup(t)
	e.svc.Schedule = func(context.Context) (backup.Schedule, error) { return backup.DefaultSchedule, nil }
	// Nothing yet, so it's due now (a backup missed while the server was down).
	if next, _ := e.svc.NextAt(t.Context()); !next.Equal(e.now) {
		t.Errorf("first next: %v", next)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if bs, _ := e.svc.List(); len(bs) == 1 && bs[0].Kind == backup.Scheduled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no scheduled backup")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if r := e.svc.LastRun(); r == nil || r.Err != nil {
		t.Errorf("last run: %+v", r)
	}
	if next, _ := e.svc.NextAt(t.Context()); !next.Equal(time.Date(2026, 10, 7, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("next after a backup: %v", next)
	}
}

// TestRestore is the restore procedure in docs/backups.md: stage a backup,
// restart, and the server runs on it.
func TestRestore(t *testing.T) {
	e := setup(t)
	addUser(t, e.db, "alice")
	b, err := e.svc.Backup(t.Context(), backup.Manual)
	if err != nil {
		t.Fatal(err)
	}
	addUser(t, e.db, "bob")
	if p, _ := e.svc.Staged(); p != nil {
		t.Fatalf("nothing staged yet: %+v", p)
	}
	p, err := e.svc.Stage(t.Context(), b.Name)
	if err != nil {
		t.Fatal(err)
	}
	if p.From != b.Name || p.Users != 1 {
		t.Errorf("staged: %+v", p)
	}
	// The backup can go; the staged copy stays.
	if err := e.svc.Delete(b.Name); err != nil {
		t.Fatal(err)
	}
	// Restart.
	e.db.Close()
	e.now = e.now.Add(time.Minute)
	applied, err := e.svc.ApplyStaged(t.Context())
	if err != nil || applied == nil || applied.From != b.Name {
		t.Fatalf("apply: %+v, %v", applied, err)
	}
	e.open(t)
	if got := usernames(t, e.db); !slices.Equal(got, []string{"alice"}) {
		t.Errorf("restored users: %v", got)
	}
	if p, _ := e.svc.Staged(); p != nil {
		t.Errorf("still staged: %+v", p)
	}
	// What was replaced was backed up first.
	bs, _ := e.svc.List()
	if len(bs) != 1 || bs[0].Kind != backup.PreRestore {
		t.Fatalf("after restore: %+v", bs)
	}
	before, err := store.Open(t.Context(), filepath.Join(e.svc.Dir, bs[0].Name))
	if err != nil {
		t.Fatal(err)
	}
	defer before.Close()
	if got := usernames(t, before); !slices.Equal(got, []string{"alice", "bob"}) {
		t.Errorf("pre-restore backup's users: %v", got)
	}
	// Nothing staged, nothing to do.
	if p, err := e.svc.ApplyStaged(t.Context()); p != nil || err != nil {
		t.Errorf("apply again: %+v, %v", p, err)
	}
}

// TestRestoreOldBackup restores a backup an older Syncphony made, and
// migrates it, backing it up before migrating.
func TestRestoreOldBackup(t *testing.T) {
	e := setup(t)
	old := filepath.Join(t.TempDir(), "old.db")
	if err := store.OpenVersion(t.Context(), old, 1); err != nil {
		t.Fatal(err)
	}
	snap, err := backup.Verify(t.Context(), old)
	if err != nil || snap.SchemaVersion != 1 {
		t.Fatalf("old backup: %+v, %v", snap, err)
	}
	if _, err := e.svc.Stage(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	e.db.Close()
	if _, err := e.svc.ApplyStaged(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(time.Minute)
	var from, to int64
	e.open(t, store.BeforeMigrate(func(ctx context.Context, s *store.Store, f, tt int64) error {
		from, to = f, tt
		_, err := e.svc.BackupFrom(ctx, s, backup.PreUpgrade)
		return err
	}))
	if from != 1 || to != store.SchemaVersion() {
		t.Errorf("migrated from %d to %d", from, to)
	}
	addUser(t, e.db, "carol")
	kinds := map[backup.Kind]int{}
	bs, _ := e.svc.List()
	for _, b := range bs {
		kinds[b.Kind]++
	}
	if kinds[backup.PreRestore] != 1 || kinds[backup.PreUpgrade] != 1 {
		t.Errorf("backups: %+v", bs)
	}
}

func TestStageRejects(t *testing.T) {
	e := setup(t)
	junk := filepath.Join(t.TempDir(), "junk.db")
	if err := os.WriteFile(junk, []byte("not a database at all, not even close, no"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Stage(t.Context(), junk); err == nil {
		t.Error("staged junk")
	}
	if _, err := e.svc.Stage(t.Context(), "syncphony-20261006-193000-manual.db"); !errors.Is(err, backup.ErrNotFound) {
		t.Errorf("missing backup: %v", err)
	}
	if p, _ := e.svc.Staged(); p != nil {
		t.Errorf("staged: %+v", p)
	}
	b, _ := e.svc.Backup(t.Context(), backup.Manual)
	if _, err := e.svc.Stage(t.Context(), b.Name); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Cancel(); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.Staged(); p != nil {
		t.Errorf("after cancel: %+v", p)
	}
}
