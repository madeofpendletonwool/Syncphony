// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

func TestBackupsAPI(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")

	for _, r := range []struct{ method, path string }{
		{"GET", "/admin/backups"},
		{"POST", "/admin/backups"},
		{"PUT", "/admin/backups/schedule"},
		{"GET", "/admin/backups/syncphony-20261006-030000-manual.db"},
		{"DELETE", "/admin/backups/syncphony-20261006-030000-manual.db"},
		{"PUT", "/admin/restore"},
		{"DELETE", "/admin/restore"},
	} {
		var body any
		if r.method == "PUT" {
			body = map[string]any{"name": "x", "frequency": "daily", "hour": 3, "weekday": 0, "keepDaily": 7, "keepWeekly": 4, "keepMonthly": 6}
		}
		if got := bob.do(r.method, r.path, body); got.status != http.StatusForbidden {
			t.Errorf("member %s %s: %d", r.method, r.path, got.status)
		}
	}

	st := status(t, alice)
	if st.Dir != e.backups.Dir || st.Problem != nil || len(st.Backups) != 0 || st.Schedule.Frequency != "daily" || st.Schedule.Hour != 3 || st.NextAt == nil || st.VaultKeyId == "" {
		t.Fatalf("status: %+v", st)
	}

	// The schedule.
	sch := httpapi.BackupSchedule{Frequency: "weekly", Hour: 0, Weekday: 5, KeepDaily: 0, KeepWeekly: 8, KeepMonthly: 12}
	var got httpapi.BackupSchedule
	alice.want(http.StatusOK, "PUT", "/admin/backups/schedule", sch).decode(t, &got)
	if got != sch {
		t.Errorf("schedule: %+v", got)
	}
	if r := alice.do("PUT", "/admin/backups/schedule", httpapi.BackupSchedule{Frequency: "daily", Hour: 24}); r.status != http.StatusBadRequest {
		t.Errorf("hour 24: %d %s", r.status, r.body)
	}
	alice.want(http.StatusOK, "PUT", "/admin/backups/schedule", httpapi.BackupSchedule{Frequency: "off"})
	st = status(t, alice)
	if st.NextAt != nil {
		t.Errorf("off, but next at %v", st.NextAt)
	}

	// Back up, download, restore, delete.
	var b httpapi.Backup
	alice.want(http.StatusCreated, "POST", "/admin/backups", nil).decode(t, &b)
	if b.Kind != "manual" || b.Bytes == 0 {
		t.Fatalf("backup: %+v", b)
	}
	dl := alice.want(http.StatusOK, "GET", "/admin/backups/"+b.Name, nil)
	if int64(len(dl.body)) != b.Bytes || !bytes.HasPrefix(dl.body, []byte("SQLite format 3")) || dl.header.Get("Content-Disposition") == "" {
		t.Errorf("download: %d bytes, %v", len(dl.body), dl.header)
	}
	alice.want(http.StatusNotFound, "GET", "/admin/backups/syncphony.db", nil)
	alice.want(http.StatusNotFound, "PUT", "/admin/restore", map[string]string{"name": "../syncphony.db"})

	var p httpapi.PendingRestore
	alice.want(http.StatusOK, "PUT", "/admin/restore", map[string]string{"name": b.Name}).decode(t, &p)
	if p.From != b.Name || p.Users != 2 || !p.KeyMatches {
		t.Errorf("staged: %+v", p)
	}
	st = status(t, alice)
	if st.PendingRestore == nil || st.PendingRestore.From != b.Name || len(st.Backups) != 1 || st.Backups[0].Name != b.Name {
		t.Fatalf("status after staging: %+v", st)
	}
	alice.want(http.StatusNoContent, "DELETE", "/admin/restore", nil)
	alice.want(http.StatusNoContent, "DELETE", "/admin/backups/"+b.Name, nil)
	st = status(t, alice)
	if st.PendingRestore != nil || len(st.Backups) != 0 {
		t.Errorf("after cancel and delete: %+v", st)
	}
}

func status(t *testing.T, c *client) httpapi.BackupStatus {
	t.Helper()
	var st httpapi.BackupStatus
	c.want(http.StatusOK, "GET", "/admin/backups", nil).decode(t, &st)
	return st
}
