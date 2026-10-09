// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"errors"

	"github.com/madeofpendletonwool/syncphony/server/internal/queue"
)

// GetQueue returns a room's queue.
func (s *Server) GetQueue(ctx context.Context, req GetQueueRequestObject) (GetQueueResponseObject, error) {
	snap, err := s.Rooms.QueueSnapshot(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	return GetQueue200JSONResponse(s.queueFor(snap, false)), nil
}

// AddToQueue adds songs to the end of the caller's lane.
func (s *Server) AddToQueue(ctx context.Context, req AddToQueueRequestObject) (AddToQueueResponseObject, error) {
	refs := make([]queue.TrackRef, len(req.Body.Items))
	for i, it := range req.Body.Items {
		refs[i] = queue.TrackRef{LinkID: deref(it.LinkId), TrackID: deref(it.TrackId), FromItemID: deref(it.FromItemId)}
		if id := deref(it.FromPlaylistSongId); id != "" {
			t, err := s.playlistSnapshot(ctx, id)
			if err != nil {
				return nil, err
			}
			refs[i].Snapshot = t
		}
	}
	opts := queue.AddOptions{WarnDuplicates: req.Body.WarnDuplicates != nil && *req.Body.WarnDuplicates}
	snap, err := s.Queue.AddWith(ctx, req.RoomId, sessionFrom(ctx).User.ID, refs, opts)
	var dup *queue.DuplicateError
	if errors.As(err, &dup) {
		out := AddToQueueConflict{Code: "duplicate", Message: dup.Error(), Duplicates: &[]QueueDuplicate{}}
		for _, d := range dup.Duplicates {
			qd := QueueDuplicate{Title: d.Title, Item: toQueueItem(d.Item)}
			if !d.PlayedAt.IsZero() {
				qd.PlayedAt = ptr(d.PlayedAt)
			}
			*out.Duplicates = append(*out.Duplicates, qd)
		}
		return AddToQueue409JSONResponse(out), nil
	}
	if err != nil {
		return nil, err
	}
	return AddToQueue200JSONResponse(s.queueFor(snap, false)), nil
}

// MoveQueueItem moves one of the caller's songs within their lane.
func (s *Server) MoveQueueItem(ctx context.Context, req MoveQueueItemRequestObject) (MoveQueueItemResponseObject, error) {
	snap, err := s.Queue.Move(ctx, req.RoomId, sessionFrom(ctx).User.ID, req.ItemId, req.Body.Position)
	if err != nil {
		return nil, err
	}
	return MoveQueueItem200JSONResponse(s.queueFor(snap, false)), nil
}

// RemoveQueueItem removes a queued song.
func (s *Server) RemoveQueueItem(ctx context.Context, req RemoveQueueItemRequestObject) (RemoveQueueItemResponseObject, error) {
	snap, err := s.Queue.Remove(ctx, req.RoomId, sessionFrom(ctx).User.ID, req.ItemId)
	if err != nil {
		return nil, err
	}
	return RemoveQueueItem200JSONResponse(s.queueFor(snap, false)), nil
}

// RestoreQueueItems puts songs removed moments ago back where they were.
func (s *Server) RestoreQueueItems(ctx context.Context, req RestoreQueueItemsRequestObject) (RestoreQueueItemsResponseObject, error) {
	snap, err := s.Queue.Restore(ctx, req.RoomId, sessionFrom(ctx).User.ID, req.Body.ItemIds)
	if err != nil {
		return nil, err
	}
	return RestoreQueueItems200JSONResponse(s.queueFor(snap, false)), nil
}

// ClearLane removes all of the caller's waiting songs.
func (s *Server) ClearLane(ctx context.Context, req ClearLaneRequestObject) (ClearLaneResponseObject, error) {
	snap, removed, err := s.Queue.ClearLane(ctx, req.RoomId, sessionFrom(ctx).User.ID)
	if err != nil {
		return nil, err
	}
	if removed == nil {
		removed = []string{}
	}
	return ClearLane200JSONResponse{Queue: s.queueFor(snap, false), Removed: removed}, nil
}
