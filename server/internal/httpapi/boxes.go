// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Boxes in the Screens list (MAD-811, ADR 0016): a Syncphony box reports
// what its bridge says about it (version, temperature, uptime, the last
// update) up its room socket, and the room's owner can reload or restart
// it from their phone. Everything here lives in memory: a box reports
// again whenever it connects, so nothing needs storing.

// MinBoxVersion is the oldest box software this server expects. A box
// older than this shows "box software older than this server expects"
// in the Screens list. ADR 0016 records it; raise both together when the
// server starts relying on something newer in the bridge.
const MinBoxVersion = "0.2.0"

// Remote care errors.
var (
	ErrDisplayOffline = errors.New("that screen isn't connected right now")
	ErrNotABox        = errors.New("only a Syncphony box can be restarted; this screen can only reload")
)

// boxReportEvery is the most often a display's box.status counts: a
// chattier box is ignored in between.
const boxReportEvery = 5 * time.Second

// boxReportLimit caps one message up the socket.
const boxReportLimit = 8 << 10

// boxRegistry is what the server knows about its displays right now:
// whether each is connected, and what each box last reported.
type boxRegistry struct {
	mu sync.Mutex
	m  map[string]*displayLive
}

type displayLive struct {
	conns  int
	report *BoxStatusReport
	at     time.Time
}

func (b *boxRegistry) get(id string) *displayLive {
	if b.m == nil {
		b.m = map[string]*displayLive{}
	}
	l := b.m[id]
	if l == nil {
		l = &displayLive{}
		b.m[id] = l
	}
	return l
}

// connect records a display connection and reports whether it's the
// display's first (it just came online).
func (b *boxRegistry) connect(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	l := b.get(id)
	l.conns++
	return l.conns == 1
}

// disconnect records a closed connection and reports whether it was the
// display's last (it just went offline). The box's last report stays, so
// the Screens list can still say what it was.
func (b *boxRegistry) disconnect(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	l := b.m[id]
	if l == nil || l.conns == 0 {
		return false
	}
	l.conns--
	return l.conns == 0
}

// report stores a box's status. It reports whether the report counted
// (not too soon after the last), and whether anything the Screens list
// shows changed enough to tell the room.
func (b *boxRegistry) report(id string, r BoxStatusReport, now time.Time) (stored, changed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	l := b.get(id)
	if l.report != nil && now.Sub(l.at) < boxReportEvery {
		return false, false
	}
	changed = l.report == nil || boxSummary(*l.report) != boxSummary(r)
	l.report, l.at = &r, now
	return true, changed
}

// forget drops an unpaired display.
func (b *boxRegistry) forget(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.m, id)
}

// live returns a display's connection state and last box report.
func (b *boxRegistry) live(id string) (online bool, report *BoxStatusReport, at time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	l := b.m[id]
	if l == nil {
		return false, nil, time.Time{}
	}
	return l.conns > 0, l.report, l.at
}

// boxSummary is what the Screens list would show differently: a change
// in it is worth a displays.updated. The temperature only counts by
// whether it's hot, so a box warming by a degree doesn't ping the room.
func boxSummary(r BoxStatusReport) string {
	hot := r.TemperatureC != nil && *r.TemperatureC >= hotC
	upd := ""
	if r.Update != nil {
		upd = r.Update.At.String() + "/" + string(r.Update.Result)
	}
	return fmt.Sprintf("%s|%v|%v|%v|%s", r.Version, hot, r.Throttled != nil && *r.Throttled, r.UnderVoltage != nil && *r.UnderVoltage, upd)
}

// hotC is when a box counts as running hot: the Pi firmware's soft
// throttling limit.
const hotC = 80.0

// validBoxReport checks a report from a socket: bounded, and an update
// result the API knows. An over-long update error is cut down, not
// refused: it's the most useful part of a failed update.
func validBoxReport(r *BoxStatusReport) bool {
	if r.Version == "" || len(r.Version) > 64 || (r.Name != nil && len(*r.Name) > 80) {
		return false
	}
	if r.Capabilities != nil {
		if len(*r.Capabilities) > 16 {
			return false
		}
		for _, c := range *r.Capabilities {
			if len(c) > 32 {
				return false
			}
		}
	}
	if r.Update != nil {
		if !r.Update.Result.Valid() {
			return false
		}
		if r.Update.Error != nil && len(*r.Update.Error) > 400 {
			e := (*r.Update.Error)[:400]
			r.Update.Error = &e
		}
	}
	return true
}

// boxOutdated reports whether a box's version is older than MinBoxVersion.
// Versions it can't read (a dev build) aren't outdated.
func boxOutdated(version string) bool {
	have, ok := parseBoxVersion(version)
	if !ok {
		return false
	}
	want, _ := parseBoxVersion(MinBoxVersion)
	for i := range have {
		if have[i] != want[i] {
			return have[i] < want[i]
		}
	}
	return false
}

// parseBoxVersion reads the box's git-describe versions: v1.3.0,
// 1.3.0, v1.3.0-4-gabc1234, v1.3.0-dirty.
func parseBoxVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+~"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// toBoxStatus is a stored report in API form.
func toBoxStatus(r BoxStatusReport, at time.Time) *BoxStatus {
	out := &BoxStatus{
		Version: r.Version, Name: r.Name, Capabilities: []string{},
		TemperatureC: r.TemperatureC, Throttled: r.Throttled, UnderVoltage: r.UnderVoltage,
		ThrottleOccurred: r.ThrottleOccurred, UptimeSeconds: r.UptimeSeconds, Update: r.Update,
		Outdated: boxOutdated(r.Version), ReportedAt: at.UTC(),
	}
	if r.Capabilities != nil {
		out.Capabilities = *r.Capabilities
	}
	return out
}

// toDisplay is a display in API form, with what's known about it live.
func (s *Server) toDisplay(d store.Display) Display {
	out := toDisplay(d)
	online, report, at := s.boxes.live(d.ID)
	out.Online = online
	if report != nil {
		out.Box = toBoxStatus(*report, at)
	}
	return out
}

// displayMessage is a message up a paired display's socket.
type displayMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// handleDisplayMessage takes one message from a paired display's socket.
// The only one there is: a box's status. Anything else is ignored.
func (s *Server) handleDisplayMessage(roomID, displayID string, raw []byte) {
	var m displayMessage
	if err := json.Unmarshal(raw, &m); err != nil || m.Type != "box.status" {
		return
	}
	var r BoxStatusReport
	if err := json.Unmarshal(m.Data, &r); err != nil || !validBoxReport(&r) {
		return
	}
	if stored, changed := s.boxes.report(displayID, r, time.Now()); stored && changed {
		s.displaysChanged(roomID)
	}
}

// displaysChanged tells the room its displays changed.
func (s *Server) displaysChanged(roomID string) {
	s.Bus.Publish(realtime.RoomTopic(roomID), realtime.Event{Type: realtime.DisplaysUpdated, Data: DisplaysChanged{RoomId: roomID}})
}

// sendDisplayCommand sends a command to one display's connections.
func (s *Server) sendDisplayCommand(displayID string, action DisplayCommandAction) {
	s.Bus.Publish(realtime.DisplayTopic(displayID), realtime.Event{Type: realtime.DisplayCommand, Data: DisplayCommand{Action: action}})
}

// ReloadDisplay reloads one display's page, on its socket only.
func (s *Server) ReloadDisplay(ctx context.Context, req ReloadDisplayRequestObject) (ReloadDisplayResponseObject, error) {
	d, err := s.manageableDisplay(ctx, req.RoomId, req.DisplayId)
	if err != nil {
		return nil, err
	}
	if online, _, _ := s.boxes.live(d.ID); !online {
		return nil, ErrDisplayOffline
	}
	s.sendDisplayCommand(d.ID, Reload)
	return ReloadDisplay202Response{}, nil
}

// RestartDisplay reboots a Syncphony box, on its socket only.
func (s *Server) RestartDisplay(ctx context.Context, req RestartDisplayRequestObject) (RestartDisplayResponseObject, error) {
	d, err := s.manageableDisplay(ctx, req.RoomId, req.DisplayId)
	if err != nil {
		return nil, err
	}
	online, report, _ := s.boxes.live(d.ID)
	if report == nil || report.Capabilities == nil || !contains(*report.Capabilities, "reboot") {
		return nil, ErrNotABox
	}
	if !online {
		return nil, ErrDisplayOffline
	}
	s.sendDisplayCommand(d.ID, Restart)
	return RestartDisplay202Response{}, nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
