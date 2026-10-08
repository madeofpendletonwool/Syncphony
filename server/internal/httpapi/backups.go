// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/admin"
	"github.com/madeofpendletonwool/syncphony/server/internal/backup"
)

func toBackup(b backup.Backup) Backup {
	return Backup{Name: b.Name, Kind: BackupKind(b.Kind), CreatedAt: b.CreatedAt, Bytes: b.Bytes}
}

func toBackupSchedule(sch backup.Schedule) BackupSchedule {
	return BackupSchedule{
		Frequency: BackupScheduleFrequency(sch.Frequency), Hour: sch.Hour, Weekday: int(sch.Weekday),
		KeepDaily: sch.KeepDaily, KeepWeekly: sch.KeepWeekly, KeepMonthly: sch.KeepMonthly,
	}
}

func (s *Server) toPendingRestore(p backup.Pending) PendingRestore {
	keys := p.KeyIDs
	if keys == nil {
		keys = []string{}
	}
	current := s.Links.KeyID()
	return PendingRestore{
		From: p.From, StagedAt: p.StagedAt, SchemaVersion: p.SchemaVersion, Users: p.Users, Links: p.Links, KeyIds: keys,
		KeyMatches: !slices.ContainsFunc(keys, func(k string) bool { return k != current }),
	}
}

// GetBackups lists the backups, their schedule, and any restore waiting
// for a restart (admins).
func (s *Server) GetBackups(ctx context.Context, _ GetBackupsRequestObject) (GetBackupsResponseObject, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	st, err := s.Admin.Settings(ctx)
	if err != nil {
		return nil, err
	}
	bs, err := s.Backups.List()
	if err != nil {
		return nil, err
	}
	out := BackupStatus{
		Dir: s.Backups.Dir, TimeZone: s.Backups.TimeZone(), Schedule: toBackupSchedule(st.Backups),
		VaultKeyId: s.Links.KeyID(), Backups: make([]Backup, len(bs)),
	}
	for i, b := range bs {
		out.Backups[i] = toBackup(b)
	}
	if err := s.Backups.Check(); err != nil {
		out.Problem = new(err.Error())
	}
	if next, err := s.Backups.NextAt(ctx); err != nil {
		return nil, err
	} else if !next.IsZero() {
		out.NextAt = &next
	}
	if r := s.Backups.LastRun(); r != nil {
		out.LastRun = &struct {
			At    time.Time `json:"at"`
			Error *string   `json:"error,omitempty"`
		}{At: r.At}
		if r.Err != nil {
			out.LastRun.Error = new(r.Err.Error())
		}
	}
	if p, err := s.Backups.Staged(); err != nil {
		return nil, err
	} else if p != nil {
		out.PendingRestore = new(s.toPendingRestore(*p))
	}
	return GetBackups200JSONResponse(out), nil
}

// CreateBackup backs up the database now (admins).
func (s *Server) CreateBackup(ctx context.Context, _ CreateBackupRequestObject) (CreateBackupResponseObject, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	b, err := s.Backups.Backup(ctx, backup.Manual)
	if err != nil {
		return nil, err
	}
	return CreateBackup201JSONResponse(toBackup(b)), nil
}

// UpdateBackupSchedule changes when the database is backed up, and what's
// kept (admins).
func (s *Server) UpdateBackupSchedule(ctx context.Context, req UpdateBackupScheduleRequestObject) (UpdateBackupScheduleResponseObject, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	b := req.Body
	sch := backup.Schedule{
		Frequency: backup.Frequency(b.Frequency), Hour: b.Hour, Weekday: time.Weekday(b.Weekday),
		KeepDaily: b.KeepDaily, KeepWeekly: b.KeepWeekly, KeepMonthly: b.KeepMonthly,
	}
	st, err := s.Admin.Update(ctx, admin.Update{Backups: &sch})
	if err != nil {
		return nil, err
	}
	s.Backups.Kick()
	return UpdateBackupSchedule200JSONResponse(toBackupSchedule(st.Backups)), nil
}

// DownloadBackup sends a backup (admins).
func (s *Server) DownloadBackup(ctx context.Context, req DownloadBackupRequestObject) (DownloadBackupResponseObject, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	path, err := s.Backups.Path(req.Name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path) //nolint:gosec // a listed backup, by name only
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return backupFile{f: f, name: req.Name, size: info.Size()}, nil
}

// backupFile sends a backup as a download.
type backupFile struct {
	f    *os.File
	name string
	size int64
}

func (b backupFile) VisitDownloadBackupResponse(w http.ResponseWriter) error {
	defer b.f.Close()
	h := w.Header()
	h.Set("Content-Type", "application/vnd.sqlite3")
	h.Set("Content-Length", strconv.FormatInt(b.size, 10))
	h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", b.name))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, b.f); err != nil {
		slog.Debug("backup download ended early", "err", err)
	}
	return nil
}

// DeleteBackup deletes a backup (admins).
func (s *Server) DeleteBackup(ctx context.Context, req DeleteBackupRequestObject) (DeleteBackupResponseObject, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if err := s.Backups.Delete(req.Name); err != nil {
		return nil, err
	}
	return DeleteBackup204Response{}, nil
}

// StageRestore restores a backup when the server next starts (admins).
func (s *Server) StageRestore(ctx context.Context, req StageRestoreRequestObject) (StageRestoreResponseObject, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	// Only a backup in the backup folder, by name: never a path.
	if _, err := s.Backups.Get(req.Body.Name); err != nil {
		return nil, err
	}
	p, err := s.Backups.Stage(ctx, req.Body.Name)
	if err != nil {
		return nil, err
	}
	slog.Warn("a backup will be restored when the server restarts", "from", p.From, "by", sessionFrom(ctx).User.Username)
	return StageRestore200JSONResponse(s.toPendingRestore(p)), nil
}

// CancelRestore drops the restore waiting for a restart (admins).
func (s *Server) CancelRestore(ctx context.Context, _ CancelRestoreRequestObject) (CancelRestoreResponseObject, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if err := s.Backups.Cancel(); err != nil {
		return nil, err
	}
	return CancelRestore204Response{}, nil
}
