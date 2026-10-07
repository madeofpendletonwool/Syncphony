// SPDX-License-Identifier: AGPL-3.0-only

// Package admin runs the server as a whole: settings that don't belong to
// a room, and database backups.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Settings are the server's own options, stored as JSON in server_settings.
// Zero values mean the defaults.
type Settings struct {
	// InstanceName is what the app calls this server, e.g. "The Den".
	// "" means just "Syncphony".
	InstanceName string `json:"instanceName,omitempty"`
	// InviteExpiryHours is how long a new invite lasts when the admin
	// doesn't say: 1 to MaxInviteHours. 0 means DefaultInviteHours.
	InviteExpiryHours int `json:"inviteExpiryHours,omitempty"`
}

// Setting limits.
const (
	DefaultInviteHours = 7 * 24
	MaxInviteHours     = 30 * 24
	MaxInstanceName    = 40
)

// InviteExpiry is how long a new invite lasts by default.
func (s Settings) InviteExpiry() time.Duration {
	h := s.InviteExpiryHours
	if h <= 0 || h > MaxInviteHours {
		h = DefaultInviteHours
	}
	return time.Duration(h) * time.Hour
}

// InvalidInputError is a bad setting.
type InvalidInputError struct{ Message string }

func (e *InvalidInputError) Error() string { return e.Message }

// KeepBackups is how many backups Backup keeps; older ones are deleted.
const KeepBackups = 7

const (
	backupPrefix = "syncphony-"
	backupSuffix = ".db"
	backupTime   = "20060102-150405"
)

// Service manages the server's settings and backups.
type Service struct {
	db *store.Store
	// BackupDir is where backups go. Default <data dir>/backups.
	BackupDir string
	// Now is the clock. Default store.Now.
	Now func() time.Time

	backingUp sync.Mutex
}

// New returns a Service keeping backups in backupDir.
func New(db *store.Store, backupDir string) *Service {
	return &Service{db: db, BackupDir: backupDir, Now: store.Now}
}

// Settings returns the server's settings.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	var st Settings
	raw, err := s.db.GetServerSettings(ctx)
	if store.IsNotFound(err) {
		return st, nil
	} else if err != nil {
		return st, err
	}
	_ = json.Unmarshal([]byte(raw), &st)
	return st, nil
}

// Update is a change to the settings; nil fields are left alone.
type Update struct {
	InstanceName      *string
	InviteExpiryHours *int
}

// Update changes the server's settings.
func (s *Service) Update(ctx context.Context, u Update) (Settings, error) {
	st, err := s.Settings(ctx)
	if err != nil {
		return st, err
	}
	if u.InstanceName != nil {
		name := strings.TrimSpace(*u.InstanceName)
		if utf8.RuneCountInString(name) > MaxInstanceName {
			return st, &InvalidInputError{fmt.Sprintf("the server's name is at most %d characters", MaxInstanceName)}
		}
		st.InstanceName = name
	}
	if h := u.InviteExpiryHours; h != nil {
		if *h < 1 || *h > MaxInviteHours {
			return st, &InvalidInputError{fmt.Sprintf("invites last 1 to %d hours", MaxInviteHours)}
		}
		st.InviteExpiryHours = *h
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return st, err
	}
	return st, s.db.SetServerSettings(ctx, string(raw))
}

// Backup is a copy of the database.
type Backup struct {
	Name      string
	CreatedAt time.Time
	Bytes     int64
}

// Backups lists the backups in BackupDir, newest first.
func (s *Service) Backups() ([]Backup, error) {
	entries, err := os.ReadDir(s.BackupDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var out []Backup
	for _, e := range entries {
		stamp, ok := strings.CutPrefix(e.Name(), backupPrefix)
		stamp, ok2 := strings.CutSuffix(stamp, backupSuffix)
		if !ok || !ok2 || e.IsDir() {
			continue
		}
		at, err := time.Parse(backupTime, stamp)
		if err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Backup{Name: e.Name(), CreatedAt: at, Bytes: info.Size()})
	}
	slices.SortFunc(out, func(a, b Backup) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

// Backup copies the database into BackupDir, and deletes all but the
// newest KeepBackups. The copy holds linked services' credentials still
// sealed: restoring it needs the same vault key.
func (s *Service) Backup(ctx context.Context) (Backup, error) {
	s.backingUp.Lock()
	defer s.backingUp.Unlock()
	if err := os.MkdirAll(s.BackupDir, 0o700); err != nil {
		return Backup{}, err
	}
	now := s.Now().UTC().Truncate(time.Second)
	name := backupPrefix + now.Format(backupTime) + backupSuffix
	path := filepath.Join(s.BackupDir, name)
	if _, err := os.Stat(path); err == nil {
		// One a second is plenty: the last one is the same.
		return Backup{}, &InvalidInputError{"a backup was just made; try again in a moment"}
	}
	if err := s.db.BackupTo(ctx, path); err != nil {
		return Backup{}, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return Backup{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Backup{}, err
	}
	all, err := s.Backups()
	if err != nil {
		return Backup{}, err
	}
	for _, old := range all[min(len(all), KeepBackups):] {
		if err := os.Remove(filepath.Join(s.BackupDir, old.Name)); err != nil {
			return Backup{}, err
		}
	}
	return Backup{Name: name, CreatedAt: now, Bytes: info.Size()}, nil
}

// DatabaseSize is how many bytes the database takes up.
func (s *Service) DatabaseSize(ctx context.Context) (int64, error) { return s.db.Size(ctx) }
