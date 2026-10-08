// SPDX-License-Identifier: AGPL-3.0-only

// Package backup copies the database into a backup directory, on a
// schedule and on request, keeps a rotation of them, and restores one
// (ADR 0014).
//
// A backup is a single SQLite file made with VACUUM INTO, so it's
// consistent while the server runs. It holds linked services' credentials
// still sealed by the vault: restoring it needs the vault key, which is
// never written next to it.
package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Kind says why a backup was made.
type Kind string

// Kinds of backup.
const (
	// Scheduled backups are kept by the schedule's rotation.
	Scheduled Kind = "scheduled"
	// Manual backups were asked for, on the Server page or the command line.
	Manual Kind = "manual"
	// PreUpgrade backups are made before a new version migrates the database.
	PreUpgrade Kind = "pre-upgrade"
	// PreRestore backups are the database a restore replaced.
	PreRestore Kind = "pre-restore"
)

// How many of each unscheduled kind are kept, newest first.
var keepKind = map[Kind]int{Manual: 10, PreUpgrade: 3, PreRestore: 3}

const (
	prefix     = "syncphony-"
	suffix     = ".db"
	stampForm  = "20060102-150405"
	partialExt = ".partial"
)

// Backup is a copy of the database in the backup directory.
type Backup struct {
	// Name is its file name, e.g. syncphony-20261006-030000-scheduled.db.
	Name      string
	Kind      Kind
	CreatedAt time.Time
	Bytes     int64
}

// ErrNotFound means there's no backup by that name.
var ErrNotFound = errors.New("no such backup")

// InvalidInputError is a request that can't be done as asked.
type InvalidInputError struct{ Message string }

func (e *InvalidInputError) Error() string { return e.Message }

// Service makes, lists and restores backups.
type Service struct {
	// DB is the database backed up. Set it once it's open.
	DB *store.Store
	// Dir is where backups go.
	Dir string
	// DataDir holds the database, and a restore waiting for a restart.
	DataDir string
	// Schedule returns when to back up and what to keep. Default
	// DefaultSchedule.
	Schedule func(context.Context) (Schedule, error)
	// Now is the clock. Default time.Now.
	Now func() time.Time
	// Location is the time zone schedules are in. Default time.Local.
	Location *time.Location

	writing sync.Mutex
	mu      sync.Mutex
	last    *Run
	kick    chan struct{}
}

// Run is how the last scheduled backup went.
type Run struct {
	At time.Time
	// Err is why it failed, or nil.
	Err error
}

// New returns a Service writing backups to dir, for the database in dataDir.
func New(dir, dataDir string) *Service {
	return &Service{
		Dir: dir, DataDir: dataDir, Now: time.Now, Location: time.Local,
		Schedule: func(context.Context) (Schedule, error) { return DefaultSchedule, nil },
		kick:     make(chan struct{}, 1),
	}
}

// parse reads a backup's file name. Names from before kinds, like
// syncphony-20261006-193000.db, were made on request.
func parse(name string) (Kind, time.Time, bool) {
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return "", time.Time{}, false
	}
	rest, ok = strings.CutSuffix(rest, suffix)
	if !ok || len(rest) < len(stampForm) {
		return "", time.Time{}, false
	}
	at, err := time.Parse(stampForm, rest[:len(stampForm)])
	if err != nil {
		return "", time.Time{}, false
	}
	kind := Manual
	if k := rest[len(stampForm):]; k != "" {
		kind = Kind(strings.TrimPrefix(k, "-"))
		if _, known := keepKind[kind]; !known && kind != Scheduled {
			return "", time.Time{}, false
		}
	}
	return kind, at, true
}

func fileName(kind Kind, at time.Time) string {
	return prefix + at.UTC().Format(stampForm) + "-" + string(kind) + suffix
}

// List returns the backups in Dir, newest first.
func (s *Service) List() ([]Backup, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var out []Backup
	for _, e := range entries {
		kind, at, ok := parse(e.Name())
		if !ok || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Backup{Name: e.Name(), Kind: kind, CreatedAt: at, Bytes: info.Size()})
	}
	slices.SortFunc(out, func(a, b Backup) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

// Get returns the backup called name.
func (s *Service) Get(name string) (Backup, error) {
	if _, _, ok := parse(name); !ok || filepath.Base(name) != name {
		return Backup{}, ErrNotFound
	}
	all, err := s.List()
	if err != nil {
		return Backup{}, err
	}
	for _, b := range all {
		if b.Name == name {
			return b, nil
		}
	}
	return Backup{}, ErrNotFound
}

// Path is where the backup called name is.
func (s *Service) Path(name string) (string, error) {
	b, err := s.Get(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Dir, b.Name), nil
}

// Delete removes the backup called name.
func (s *Service) Delete(name string) error {
	path, err := s.Path(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// Backup copies the database into Dir now, then prunes old backups.
func (s *Service) Backup(ctx context.Context, kind Kind) (Backup, error) {
	if s.DB == nil {
		return Backup{}, errors.New("backup: the database isn't open")
	}
	return s.write(ctx, kind, s.DB.BackupTo)
}

// BackupFrom copies src into Dir: for backing up before migrating, when
// DB isn't set yet.
func (s *Service) BackupFrom(ctx context.Context, src *store.Store, kind Kind) (Backup, error) {
	return s.write(ctx, kind, src.BackupTo)
}

// backupFile copies the database file at path into Dir.
func (s *Service) backupFile(ctx context.Context, path string, kind Kind) (Backup, error) {
	return s.write(ctx, kind, func(ctx context.Context, dst string) error { return store.Copy(ctx, path, dst) })
}

// write makes a backup with copyTo, under a temporary name until it's
// checked, so a half-written or damaged copy never looks like a backup.
func (s *Service) write(ctx context.Context, kind Kind, copyTo func(context.Context, string) error) (Backup, error) {
	s.writing.Lock()
	defer s.writing.Unlock()
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return Backup{}, err
	}
	now := s.Now().UTC().Truncate(time.Second)
	name := fileName(kind, now)
	path := filepath.Join(s.Dir, name)
	if _, err := os.Stat(path); err == nil {
		// One a second is plenty: the last one is the same.
		return Backup{}, &InvalidInputError{"a backup was just made; try again in a moment"}
	}
	tmp := filepath.Join(s.Dir, "."+name+partialExt)
	_ = os.Remove(tmp)
	err := func() error {
		if err := copyTo(ctx, tmp); err != nil {
			return err
		}
		if _, err := store.Inspect(ctx, tmp, false); err != nil {
			return fmt.Errorf("checking the copy: %w", err)
		}
		if err := os.Chmod(tmp, 0o600); err != nil {
			return err
		}
		return os.Rename(tmp, path)
	}()
	if err != nil {
		_ = os.Remove(tmp)
		return Backup{}, fmt.Errorf("backing up to %s: %w", s.Dir, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Backup{}, err
	}
	if err := s.prune(ctx); err != nil {
		return Backup{}, err
	}
	return Backup{Name: name, Kind: kind, CreatedAt: now, Bytes: info.Size()}, nil
}

// prune deletes the backups the schedule's rotation doesn't keep.
func (s *Service) prune(ctx context.Context) error {
	sch, err := s.Schedule(ctx)
	if err != nil {
		return err
	}
	all, err := s.List()
	if err != nil {
		return err
	}
	for _, b := range Prune(all, sch, s.Now(), s.Location) {
		if err := os.Remove(filepath.Join(s.Dir, b.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Prune returns the backups to delete from all (newest first): scheduled
// ones are kept by sch's rotation, the others are kept a few of each kind.
func Prune(all []Backup, sch Schedule, now time.Time, loc *time.Location) []Backup {
	var drop []Backup
	seen := map[Kind]int{}
	var days, weeks, months []string
	keep := func(b Backup) bool {
		if b.Kind != Scheduled {
			seen[b.Kind]++
			return seen[b.Kind] <= keepKind[b.Kind]
		}
		seen[Scheduled]++
		// The newest, and everything from the last day.
		kept := seen[Scheduled] == 1 || now.Sub(b.CreatedAt) < 24*time.Hour
		// Then the newest of each day, week and month, as many as are kept.
		t := b.CreatedAt.In(loc)
		y, w := t.ISOWeek()
		for _, slot := range []struct {
			key  string
			seen *[]string
			keep int
		}{
			{t.Format("2006-01-02"), &days, sch.KeepDaily},
			{fmt.Sprintf("%d-W%02d", y, w), &weeks, sch.KeepWeekly},
			{t.Format("2006-01"), &months, sch.KeepMonthly},
		} {
			if !slices.Contains(*slot.seen, slot.key) && len(*slot.seen) < slot.keep {
				*slot.seen = append(*slot.seen, slot.key)
				kept = true
			}
		}
		return kept
	}
	for _, b := range all {
		if !keep(b) {
			drop = append(drop, b)
		}
	}
	return drop
}

// Check makes sure backups can be written to Dir.
func (s *Service) Check() error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("can't create the backup folder %s: %w", s.Dir, err)
	}
	f, err := os.CreateTemp(s.Dir, ".probe-*")
	if err != nil {
		return fmt.Errorf("can't write to the backup folder %s (in Docker, is it owned by UID 65532?): %w", s.Dir, err)
	}
	f.Close()
	return os.Remove(f.Name())
}

// CleanUp removes copies left half-written by a crash.
func (s *Service) CleanUp() {
	entries, _ := os.ReadDir(s.Dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "."+prefix) && strings.HasSuffix(e.Name(), partialExt) {
			_ = os.Remove(filepath.Join(s.Dir, e.Name()))
		}
	}
}
