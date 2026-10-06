// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/madeofpendletonwool/syncphony/server/internal/lyrics"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// GetTrackLyrics returns a track's lyrics through a link the caller may use.
func (s *Server) GetTrackLyrics(ctx context.Context, req GetTrackLyricsRequestObject) (GetTrackLyricsResponseObject, error) {
	l, sess, err := s.openUsable(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	r, err := s.Lyrics.Get(ctx, sess, provider.Track{Ref: provider.TrackRef{Provider: l.Provider, LinkID: l.ID, ID: req.TrackId}})
	if err != nil {
		return nil, err
	}
	return GetTrackLyrics200JSONResponse(toLyrics(r)), nil
}

// GetQueueItemLyrics returns a queued song's lyrics through the link of
// whoever queued it, so the whole room can read along.
func (s *Server) GetQueueItemLyrics(ctx context.Context, req GetQueueItemLyricsRequestObject) (GetQueueItemLyricsResponseObject, error) {
	it, err := s.Queue.Item(ctx, req.RoomId, req.ItemId)
	if err != nil {
		return nil, err
	}
	// metadata is the provider.Track snapshot taken when the item was queued.
	var t provider.Track
	if err := json.Unmarshal([]byte(it.Metadata), &t); err != nil {
		slog.Warn("queue item metadata", "item", it.ID, "err", err)
	}
	t.Ref = provider.TrackRef{Provider: it.Provider, LinkID: it.LinkID.String, ID: it.TrackID}
	// Without the link, LRCLIB can still go by the metadata.
	var sess provider.Session
	if it.LinkID.Valid {
		if sess, err = s.Links.Open(ctx, it.LinkID.String); err != nil {
			slog.Debug("lyrics: opening the queuer's link", "link", it.LinkID.String, "err", err)
			sess = nil
		} else {
			defer sess.Close()
		}
	}
	r, err := s.Lyrics.Get(ctx, sess, t)
	if err != nil {
		return nil, err
	}
	return GetQueueItemLyrics200JSONResponse(toLyrics(r)), nil
}

func toLyrics(r lyrics.Result) Lyrics {
	out := Lyrics{Source: r.Source, Synced: len(r.Synced) > 0, Instrumental: r.Instrumental, Plain: r.Plain, Lines: make([]LyricLine, len(r.Synced))}
	for i, l := range r.Synced {
		out.Lines[i] = LyricLine{AtMs: l.At.Milliseconds(), Text: l.Text}
	}
	return out
}
