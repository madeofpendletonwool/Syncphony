// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/madeofpendletonwool/syncphony/server/internal/linernotes"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/quiz"
)

// GetQueueItemLinerNotes returns a queued song's liner notes.
func (s *Server) GetQueueItemLinerNotes(ctx context.Context, req GetQueueItemLinerNotesRequestObject) (GetQueueItemLinerNotesResponseObject, error) {
	if s.hidden(req.RoomId, req.ItemId, quiz.HideNotes) || s.hidden(req.RoomId, req.ItemId, quiz.HideSong) {
		return nil, ErrHiddenForRound
	}
	it, err := s.Queue.Item(ctx, req.RoomId, req.ItemId)
	if err != nil {
		return nil, err
	}
	if s.LinerNotes == nil {
		return nil, fmt.Errorf("liner notes: MusicBrainz is off: %w", provider.ErrNotFound)
	}
	t, err := queuedTrack(it)
	if err != nil {
		slog.Warn("queue item metadata", "item", it.ID, "err", err)
	}
	n, err := s.LinerNotes.Get(ctx, t)
	if err != nil {
		return nil, err
	}
	return GetQueueItemLinerNotes200JSONResponse(toLinerNotes(n)), nil
}

func toLinerNotes(n linernotes.Notes) LinerNotes {
	out := LinerNotes{
		Title: n.Title, RecordingMbid: n.RecordingMBID,
		Credits: make([]LinerNotesCredit, len(n.Credits)), Facts: make([]LinerNotesFact, len(n.Facts)),
	}
	if n.Year > 0 {
		out.Year = &n.Year
	}
	if r := n.Release; r != nil {
		out.Release = &LinerNotesRelease{Title: r.Title, Labels: r.Labels}
		if out.Release.Labels == nil {
			out.Release.Labels = []string{}
		}
		if r.Type != "" {
			out.Release.Type = &r.Type
		}
		if r.Date != "" {
			out.Release.Date = &r.Date
		}
	}
	if a := n.Artist; a != nil {
		out.Artist = &LinerNotesArtist{Mbid: a.MBID, Name: a.Name}
		if a.About != "" {
			out.Artist.About = &a.About
		}
		if a.Bio != "" {
			out.Artist.Bio = &a.Bio
		}
		if a.BioURL != "" {
			out.Artist.BioUrl = &a.BioURL
		}
	}
	for i, c := range n.Credits {
		out.Credits[i] = LinerNotesCredit{Role: c.Role, Names: c.Names}
	}
	for i, f := range n.Facts {
		out.Facts[i] = LinerNotesFact{Kind: LinerNotesFactKind(f.Kind), Text: f.Text}
	}
	return out
}
