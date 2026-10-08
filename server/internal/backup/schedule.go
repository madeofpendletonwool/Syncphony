// SPDX-License-Identifier: AGPL-3.0-only

package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// Frequency is how often scheduled backups are made.
type Frequency string

// Frequencies.
const (
	Off      Frequency = "off"
	Every6h  Frequency = "6h"
	Every12h Frequency = "12h"
	Daily    Frequency = "daily"
	Weekly   Frequency = "weekly"
)

// Schedule says when to back up, and which scheduled backups to keep.
type Schedule struct {
	Frequency Frequency `json:"frequency"`
	// Hour is the hour of the day (0-23, server time) daily and weekly
	// backups are made at; every 6 or 12 hours counts from it.
	Hour int `json:"hour"`
	// Weekday is the day weekly backups are made on.
	Weekday time.Weekday `json:"weekday"`
	// KeepDaily, KeepWeekly and KeepMonthly are how many days, weeks and
	// months keep their newest scheduled backup. Every backup from the
	// last day, and the newest, are kept too.
	KeepDaily   int `json:"keepDaily"`
	KeepWeekly  int `json:"keepWeekly"`
	KeepMonthly int `json:"keepMonthly"`
}

// DefaultSchedule is a nightly backup at 3am, keeping a week of days, a
// month of weeks and half a year of months.
var DefaultSchedule = Schedule{Frequency: Daily, Hour: 3, Weekday: time.Sunday, KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6}

// Limits on what's kept.
const (
	MaxKeepDaily   = 90
	MaxKeepWeekly  = 52
	MaxKeepMonthly = 36
)

// Validate says what's wrong with sch, if anything.
func (sch Schedule) Validate() error {
	switch sch.Frequency {
	case Off, Every6h, Every12h, Daily, Weekly:
	default:
		return &InvalidInputError{fmt.Sprintf("backups can't be made %q", sch.Frequency)}
	}
	switch {
	case sch.Hour < 0 || sch.Hour > 23:
		return &InvalidInputError{"the hour is 0 to 23"}
	case sch.Weekday < time.Sunday || sch.Weekday > time.Saturday:
		return &InvalidInputError{"the weekday is 0 (Sunday) to 6"}
	case sch.KeepDaily < 0 || sch.KeepDaily > MaxKeepDaily:
		return &InvalidInputError{fmt.Sprintf("keep 0 to %d daily backups", MaxKeepDaily)}
	case sch.KeepWeekly < 0 || sch.KeepWeekly > MaxKeepWeekly:
		return &InvalidInputError{fmt.Sprintf("keep 0 to %d weekly backups", MaxKeepWeekly)}
	case sch.KeepMonthly < 0 || sch.KeepMonthly > MaxKeepMonthly:
		return &InvalidInputError{fmt.Sprintf("keep 0 to %d monthly backups", MaxKeepMonthly)}
	}
	return nil
}

// slotsOn returns the times on day's date that backups are due, in order.
func (sch Schedule) slotsOn(day time.Time) []time.Time {
	at := func(h int) time.Time {
		return time.Date(day.Year(), day.Month(), day.Day(), h, 0, 0, 0, day.Location())
	}
	switch sch.Frequency {
	case Daily:
		return []time.Time{at(sch.Hour)}
	case Weekly:
		if day.Weekday() == sch.Weekday {
			return []time.Time{at(sch.Hour)}
		}
	case Every6h, Every12h:
		step := 6
		if sch.Frequency == Every12h {
			step = 12
		}
		var out []time.Time
		for h := sch.Hour % step; h < 24; h += step {
			out = append(out, at(h))
		}
		return out
	}
	return nil
}

// Last is the latest time a backup was due, at or before now; zero when
// backups are off.
func (sch Schedule) Last(now time.Time) time.Time {
	for d := 0; d <= 7; d++ {
		slots := sch.slotsOn(now.AddDate(0, 0, -d))
		for i := len(slots) - 1; i >= 0; i-- {
			if !slots[i].After(now) {
				return slots[i]
			}
		}
	}
	return time.Time{}
}

// Next is the first time a backup is due after now; zero when backups are off.
func (sch Schedule) Next(now time.Time) time.Time {
	for d := 0; d <= 7; d++ {
		for _, t := range sch.slotsOn(now.AddDate(0, 0, d)) {
			if t.After(now) {
				return t
			}
		}
	}
	return time.Time{}
}

// retryAfter is how long a failed scheduled backup waits to try again.
const retryAfter = 15 * time.Minute

// Run makes scheduled backups until ctx is done. A backup missed while
// the server was down is made when it starts.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if err := s.tick(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("checking the backup schedule", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
		}
	}
}

// Kick has Run look at the schedule again now, after it changed.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// tick makes a scheduled backup if one is due.
func (s *Service) tick(ctx context.Context) error {
	due, err := s.due(ctx)
	if err != nil || !due {
		return err
	}
	if last := s.LastRun(); last != nil && last.Err != nil && s.Now().Sub(last.At) < retryAfter {
		return nil
	}
	b, err := s.Backup(ctx, Scheduled)
	s.mu.Lock()
	s.last = &Run{At: s.Now().UTC(), Err: err}
	s.mu.Unlock()
	if err != nil {
		slog.Error("scheduled backup failed; trying again in 15 minutes", "err", err)
		return nil
	}
	slog.Info("backed up the database", "file", b.Name, "bytes", b.Bytes)
	return nil
}

// due reports whether a scheduled backup is due: the schedule has had a
// slot since the newest one.
func (s *Service) due(ctx context.Context) (bool, error) {
	sch, err := s.Schedule(ctx)
	if err != nil {
		return false, err
	}
	slot := sch.Last(s.Now().In(s.Location))
	if slot.IsZero() {
		return false, nil
	}
	all, err := s.List()
	if err != nil {
		return false, err
	}
	for _, b := range all {
		if b.Kind == Scheduled {
			return b.CreatedAt.Before(slot), nil
		}
	}
	return true, nil
}

// LastRun is how the last scheduled backup since the server started
// went, or nil.
func (s *Service) LastRun() *Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// NextAt is when the next scheduled backup is due: now if one is overdue,
// zero if backups are off.
func (s *Service) NextAt(ctx context.Context) (time.Time, error) {
	sch, err := s.Schedule(ctx)
	if err != nil {
		return time.Time{}, err
	}
	now := s.Now().In(s.Location)
	if due, err := s.due(ctx); err != nil || due {
		return now, err
	}
	return sch.Next(now), nil
}

// TimeZone names the time zone schedules are in, e.g. "Europe/London",
// or "UTC" in a container without TZ set.
func (s *Service) TimeZone() string {
	if name := s.Location.String(); name != "Local" {
		return name
	}
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	name, _ := s.Now().In(s.Location).Zone()
	return name
}
