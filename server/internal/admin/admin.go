// SPDX-License-Identifier: AGPL-3.0-only

// Package admin keeps the server's own settings: those that don't belong
// to a room.
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/madeofpendletonwool/syncphony/server/internal/backup"
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
	// Backups says when the database is backed up, and what's kept.
	Backups backup.Schedule `json:"backups"`
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

// Service manages the server's settings.
type Service struct {
	db *store.Store
}

// New returns a Service.
func New(db *store.Store) *Service { return &Service{db: db} }

// Settings returns the server's settings.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	st := Settings{Backups: backup.DefaultSchedule}
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
	Backups           *backup.Schedule
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
	if b := u.Backups; b != nil {
		if err := b.Validate(); err != nil {
			return st, err
		}
		st.Backups = *b
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return st, err
	}
	return st, s.db.SetServerSettings(ctx, string(raw))
}

// DatabaseSize is how many bytes the database takes up.
func (s *Service) DatabaseSize(ctx context.Context) (int64, error) { return s.db.Size(ctx) }

// BackupSchedule is when the database is backed up, and what's kept: for
// backup.Service.Schedule.
func (s *Service) BackupSchedule(ctx context.Context) (backup.Schedule, error) {
	st, err := s.Settings(ctx)
	return st.Backups, err
}
