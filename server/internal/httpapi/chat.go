// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/chat"
)

// Room chat (MAD-842, ADR 0018): members and guests chat in their room,
// paired displays read along.

// chatRate: messages per person, a smaller burst than reactions'.
var chatRate = rate{burst: 5, every: 2 * time.Second}

// ListChatMessages pages the room's chat, newest first.
func (s *Server) ListChatMessages(ctx context.Context, req ListChatMessagesRequestObject) (ListChatMessagesResponseObject, error) {
	if _, err := s.Rooms.Get(ctx, req.RoomId); err != nil {
		return nil, err
	}
	limit := 0
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	ms, err := s.Chat.List(ctx, req.RoomId, deref(req.Params.Before), limit)
	if err != nil {
		return nil, err
	}
	out := make(ListChatMessages200JSONResponse, len(ms))
	for i, m := range ms {
		out[i] = toChatMessage(m)
	}
	return out, nil
}

// SendChatMessage posts to the room's chat, with the song that's playing.
func (s *Server) SendChatMessage(ctx context.Context, req SendChatMessageRequestObject) (SendChatMessageResponseObject, error) {
	if _, err := s.Rooms.Get(ctx, req.RoomId); err != nil {
		return nil, err
	}
	u := sessionFrom(ctx).User
	if wait, ok := s.chats.allow(u.ID, time.Now(), chatRate); !ok {
		return nil, &auth.RateLimitError{RetryAfter: wait}
	}
	itemID := ""
	if s.Playback != nil {
		if np, err := s.Playback.NowPlaying(ctx, req.RoomId); err == nil && np.Item != nil {
			itemID = np.Item.ID
		}
	}
	m, err := s.Chat.Send(ctx, req.RoomId, u.ID, req.Body.Body, itemID)
	if err != nil {
		return nil, err
	}
	return SendChatMessage201JSONResponse(toChatMessage(m)), nil
}

// DeleteChatMessage deletes a message: your own, or anyone's in a room
// you host.
func (s *Server) DeleteChatMessage(ctx context.Context, req DeleteChatMessageRequestObject) (DeleteChatMessageResponseObject, error) {
	room, err := s.Rooms.Get(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	u := sessionFrom(ctx).User
	if err := s.Chat.Delete(ctx, req.RoomId, req.MessageId, u.ID, mayHost(u, room)); err != nil {
		return nil, err
	}
	return DeleteChatMessage204Response{}, nil
}

func toChatMessage(m chat.Message) ChatMessage {
	out := ChatMessage{Id: m.ID, RoomId: m.RoomID, UserId: m.UserID, Body: m.Body, CreatedAt: m.CreatedAt}
	if m.NightID.Valid {
		out.NightId = &m.NightID.String
	}
	if m.Item != nil {
		t := toQueueItem(*m.Item).Track
		out.ItemId, out.Track = &m.Item.ID, &t
	}
	return out
}
