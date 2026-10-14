// SPDX-License-Identifier: AGPL-3.0-only

// Package chat keeps a room's chat (MAD-842, ADR 0018): plain-text
// messages to everyone in the room, each with the song that was playing.
// Messages are kept with the night they were sent in (nights gives them
// theirs when it ends) and reach every screen over the room's topic.
package chat

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Errors returned by Service.
var (
	ErrNotFound  = errors.New("message not found")
	ErrForbidden = errors.New("only its sender, the room's owner or an admin can delete a message")
)

// InvalidInputError is a message that can't be sent, such as an empty one.
type InvalidInputError struct{ Message string }

func (e *InvalidInputError) Error() string { return e.Message }

// Limits.
const (
	// MaxBody is the longest message, in characters.
	MaxBody = 500
	// DefaultPage and MaxPage are how many messages a page holds.
	DefaultPage = 50
	MaxPage     = 100
)

// Message is a chat message, with the song that was playing when it was
// sent, if it's still in the room. Published as realtime.ChatMessage.
type Message struct {
	store.ChatMessage
	Item *store.QueueItem
}

// Deleted is a message that was deleted. Published as
// realtime.ChatMessageDeleted.
type Deleted struct {
	RoomID, ID string
}

// Service keeps rooms' chat.
type Service struct {
	db  *store.Store
	bus realtime.Bus
	// Now is the clock. Default store.Now.
	Now func() time.Time
}

// New returns a Service.
func New(db *store.Store, bus realtime.Bus) *Service {
	return &Service{db: db, bus: bus, Now: store.Now}
}

// Send posts a message to a room as userID. itemID is the song playing,
// "" if none.
func (s *Service) Send(ctx context.Context, roomID, userID, body, itemID string) (Message, error) {
	body = strings.TrimSpace(body)
	switch n := utf8.RuneCountInString(body); {
	case !utf8.ValidString(body):
		return Message{}, &InvalidInputError{"that message isn't valid text"}
	case n == 0:
		return Message{}, &InvalidInputError{"say something first"}
	case n > MaxBody:
		return Message{}, &InvalidInputError{"messages are 500 characters at most"}
	}
	m := store.ChatMessage{ID: store.NewID(), RoomID: roomID, UserID: userID, Body: body, CreatedAt: s.Now()}
	var item *store.QueueItem
	if itemID != "" {
		if it, err := s.db.GetQueueItem(ctx, itemID); err == nil && it.RoomID == roomID {
			m.QueueItemID, item = sql.NullString{String: itemID, Valid: true}, &it
		}
	}
	err := s.db.InsertChatMessage(ctx, store.InsertChatMessageParams{
		ID: m.ID, RoomID: m.RoomID, UserID: m.UserID, Body: m.Body, QueueItemID: m.QueueItemID, CreatedAt: m.CreatedAt,
	})
	if err != nil {
		return Message{}, err
	}
	out := Message{ChatMessage: m, Item: item}
	s.bus.Publish(realtime.RoomTopic(roomID), realtime.Event{Type: realtime.ChatMessage, Data: out})
	return out, nil
}

// List returns a page of a room's messages, newest first: those before
// the message before ("" for the newest).
func (s *Service) List(ctx context.Context, roomID, before string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = DefaultPage
	}
	rows, err := s.db.ListChatMessages(ctx, store.ListChatMessagesParams{RoomID: roomID, Before: before, Limit: int64(min(limit, MaxPage))})
	if err != nil {
		return nil, err
	}
	// Chat runs through a song, so a page names few of them.
	items := map[string]*store.QueueItem{}
	out := make([]Message, len(rows))
	for i, m := range rows {
		out[i] = Message{ChatMessage: m}
		if !m.QueueItemID.Valid {
			continue
		}
		id := m.QueueItemID.String
		it, ok := items[id]
		if !ok {
			if row, err := s.db.GetQueueItem(ctx, id); err == nil {
				it = &row
			} else if !store.IsNotFound(err) {
				return nil, err
			}
			items[id] = it
		}
		out[i].Item = it
	}
	return out, nil
}

// Delete deletes a message. Its sender may, and so may a moderator: the
// room's owner or an admin.
func (s *Service) Delete(ctx context.Context, roomID, messageID, userID string, moderator bool) error {
	m, err := s.db.GetChatMessage(ctx, store.GetChatMessageParams{ID: messageID, RoomID: roomID})
	if store.IsNotFound(err) || (err == nil && m.DeletedAt.Valid) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if m.UserID != userID && !moderator {
		return ErrForbidden
	}
	if err := s.db.DeleteChatMessage(ctx, store.DeleteChatMessageParams{DeletedAt: sql.NullTime{Time: s.Now(), Valid: true}, ID: m.ID}); err != nil {
		return err
	}
	s.bus.Publish(realtime.RoomTopic(roomID), realtime.Event{Type: realtime.ChatMessageDeleted, Data: Deleted{RoomID: roomID, ID: m.ID}})
	return nil
}
