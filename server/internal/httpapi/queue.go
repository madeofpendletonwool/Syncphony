// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"

	"github.com/madeofpendletonwool/syncphony/server/internal/queue"
)

// GetQueue returns a room's queue.
func (s *Server) GetQueue(ctx context.Context, req GetQueueRequestObject) (GetQueueResponseObject, error) {
	snap, err := s.Rooms.QueueSnapshot(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	return GetQueue200JSONResponse(toQueueSnapshot(snap)), nil
}

// AddToQueue adds songs to the end of the caller's lane.
func (s *Server) AddToQueue(ctx context.Context, req AddToQueueRequestObject) (AddToQueueResponseObject, error) {
	refs := make([]queue.TrackRef, len(req.Body.Items))
	for i, it := range req.Body.Items {
		refs[i] = queue.TrackRef{LinkID: deref(it.LinkId), TrackID: deref(it.TrackId), FromItemID: deref(it.FromItemId)}
	}
	snap, err := s.Queue.Add(ctx, req.RoomId, sessionFrom(ctx).User.ID, refs)
	if err != nil {
		return nil, err
	}
	return AddToQueue200JSONResponse(toQueueSnapshot(snap)), nil
}

// MoveQueueItem moves one of the caller's songs within their lane.
func (s *Server) MoveQueueItem(ctx context.Context, req MoveQueueItemRequestObject) (MoveQueueItemResponseObject, error) {
	snap, err := s.Queue.Move(ctx, req.RoomId, sessionFrom(ctx).User.ID, req.ItemId, req.Body.Position)
	if err != nil {
		return nil, err
	}
	return MoveQueueItem200JSONResponse(toQueueSnapshot(snap)), nil
}

// RemoveQueueItem removes a queued song.
func (s *Server) RemoveQueueItem(ctx context.Context, req RemoveQueueItemRequestObject) (RemoveQueueItemResponseObject, error) {
	snap, err := s.Queue.Remove(ctx, req.RoomId, sessionFrom(ctx).User.ID, req.ItemId)
	if err != nil {
		return nil, err
	}
	return RemoveQueueItem200JSONResponse(toQueueSnapshot(snap)), nil
}

// RestoreQueueItems puts songs removed moments ago back where they were.
func (s *Server) RestoreQueueItems(ctx context.Context, req RestoreQueueItemsRequestObject) (RestoreQueueItemsResponseObject, error) {
	snap, err := s.Queue.Restore(ctx, req.RoomId, sessionFrom(ctx).User.ID, req.Body.ItemIds)
	if err != nil {
		return nil, err
	}
	return RestoreQueueItems200JSONResponse(toQueueSnapshot(snap)), nil
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
	return ClearLane200JSONResponse{Queue: toQueueSnapshot(snap), Removed: removed}, nil
}
