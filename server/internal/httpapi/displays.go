// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Big-screen displays (MAD-716): pairing a TV with a room, and the emoji
// reactions phones send to it.

// Cookie names. They must match the "display" security scheme in
// api/openapi.yaml, and the pairing cookie the display polls with.
const (
	DisplayCookie = "syncphony_display"
	pairingCookie = "syncphony_display_pairing"
)

// displayOps can be called by a paired display, for its own room. They
// carry `display: []` among their security requirements in the spec;
// TestDisplayOpsMatchSpec keeps the two in sync.
var displayOps = map[string]bool{
	"GetRoom":                true,
	"GetQueue":               true,
	"GetPlayback":            true,
	"ListUsers":              true,
	"GetUserAvatar":          true,
	"GetQueueItemArtwork":    true,
	"GetQueueItemPalette":    true,
	"GetQueueItemBeatMap":    true,
	"GetQueueItemLyrics":     true,
	"GetQueueItemLinerNotes": true,
	"GetHearts":              true,
	"GetGuestPass":           true,
	// Playing the room's audio, for displays with audio on (audioOps).
	"ClaimPlayer":     true,
	"ReleasePlayer":   true,
	"ReportPlayback":  true,
	"StreamQueueItem": true,
}

// audioOps are the displayOps that make a display the room's speaker. They
// need the display's audio turned on.
var audioOps = map[string]bool{
	"ClaimPlayer":     true,
	"ReleasePlayer":   true,
	"ReportPlayback":  true,
	"StreamQueueItem": true,
}

// Display errors.
var (
	ErrDisplayNoAudio = errors.New("this screen isn't set to play the room's audio")
	ErrDisplayOrphan  = errors.New("whoever paired this screen is gone; pair it again to play audio on it")
)

// displayRoom is the room a display-readable request is about. ok is
// false for requests not about one room (the user list).
func displayRoom(req any) (roomID string, ok bool) {
	switch r := req.(type) {
	case GetRoomRequestObject:
		return r.RoomId, true
	case GetQueueRequestObject:
		return r.RoomId, true
	case GetPlaybackRequestObject:
		return r.RoomId, true
	case GetQueueItemArtworkRequestObject:
		return r.RoomId, true
	case GetQueueItemPaletteRequestObject:
		return r.RoomId, true
	case GetQueueItemBeatMapRequestObject:
		return r.RoomId, true
	case GetQueueItemLyricsRequestObject:
		return r.RoomId, true
	case GetQueueItemLinerNotesRequestObject:
		return r.RoomId, true
	case GetHeartsRequestObject:
		return r.RoomId, true
	case GetGuestPassRequestObject:
		return r.RoomId, true
	case ClaimPlayerRequestObject:
		return r.RoomId, true
	case ReleasePlayerRequestObject:
		return r.RoomId, true
	case ReportPlaybackRequestObject:
		return r.RoomId, true
	case StreamQueueItemRequestObject:
		return r.RoomId, true
	}
	return "", false
}

// authenticateDisplay lets a paired display through to an operation in
// displayOps, if it's about the display's own room, and to audioOps if its
// audio is on.
func (s *Server) authenticateDisplay(ctx context.Context, w http.ResponseWriter, r *http.Request, operationID string, req any) (context.Context, error) {
	c, err := r.Cookie(DisplayCookie)
	if err != nil {
		return nil, auth.ErrUnauthenticated
	}
	d, err := s.Auth.AuthenticateDisplay(ctx, c.Value)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthenticated) {
			http.SetCookie(w, s.clearDisplayCookie())
		}
		return nil, err
	}
	if d.Token != "" {
		http.SetCookie(w, s.displayCookie(d.Token, d.Display.ExpiresAt))
	}
	if room, ok := displayRoom(req); ok && room != d.Display.RoomID {
		return nil, auth.ErrForbidden
	}
	if audioOps[operationID] && !d.Display.Audio {
		return nil, ErrDisplayNoAudio
	}
	return context.WithValue(ctx, ctxDisplay, &d.Display), nil
}

// displayFrom returns the paired display making the request, or nil for a
// signed-in user.
func displayFrom(ctx context.Context) *store.Display {
	d, _ := ctx.Value(ctxDisplay).(*store.Display)
	return d
}

// speakerOf is who a speaker request acts as, and for which device. A
// display acts as whoever paired it, and is always its own device.
func speakerOf(ctx context.Context, deviceID string) (userID, device string, err error) {
	if d := displayFrom(ctx); d != nil {
		if !d.PairedBy.Valid {
			return "", "", ErrDisplayOrphan
		}
		return d.PairedBy.String, d.ID, nil
	}
	return sessionFrom(ctx).User.ID, deviceID, nil
}

func (s *Server) displayCookie(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // Secure is set when served over HTTPS
		Name: DisplayCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: s.secure(), SameSite: http.SameSiteLaxMode,
	}
}

func (s *Server) clearDisplayCookie() *http.Cookie {
	return &http.Cookie{ //nolint:gosec // as above
		Name: DisplayCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure(), SameSite: http.SameSiteLaxMode,
	}
}

// pairingCookie holds a display's pairing secret for ttl; an empty secret clears it.
func (s *Server) pairingCookie(secret string, ttl time.Duration) *http.Cookie {
	c := &http.Cookie{ //nolint:gosec // as above
		Name: pairingCookie, Value: secret, Path: "/api/display", MaxAge: int(ttl.Seconds()),
		HttpOnly: true, Secure: s.secure(), SameSite: http.SameSiteLaxMode,
	}
	if secret == "" || c.MaxAge <= 0 {
		c.MaxAge = -1
	}
	return c
}

func toDisplay(d store.Display) Display {
	out := Display{Id: d.ID, RoomId: d.RoomID, Name: d.Name, Audio: d.Audio, CreatedAt: d.CreatedAt, LastSeenAt: d.LastSeenAt}
	if d.PairedBy.Valid {
		out.PairedBy = &d.PairedBy.String
	}
	return out
}

// BeginDisplayPairing gives a display a code to show.
func (s *Server) BeginDisplayPairing(ctx context.Context, _ BeginDisplayPairingRequestObject) (BeginDisplayPairingResponseObject, error) {
	p, err := s.Auth.BeginDisplayPairing()
	if err != nil {
		return nil, err
	}
	requestFrom(ctx).setCookie(s.pairingCookie(p.Secret, p.Expires.Sub(p.Begun)))
	return BeginDisplayPairing201JSONResponse{Code: p.Code, ExpiresAt: p.Expires}, nil
}

// PollDisplayPairing tells a display whether its code was typed in, and
// signs it in once it was.
func (s *Server) PollDisplayPairing(ctx context.Context, _ PollDisplayPairingRequestObject) (PollDisplayPairingResponseObject, error) {
	r := requestFrom(ctx)
	p, err := s.Auth.PollDisplayPairing(r.pairing)
	if err != nil {
		r.setCookie(s.pairingCookie("", 0))
		return nil, err
	}
	out := PollDisplayPairing200JSONResponse{Status: Waiting, Code: p.Code, ExpiresAt: p.Expires}
	if p.Token != "" {
		r.setCookie(s.pairingCookie("", 0))
		r.setCookie(s.displayCookie(p.Token, p.Display.ExpiresAt))
		out.Status, out.RoomId = Paired, &p.Display.RoomID
	}
	return out, nil
}

// GetDisplay returns the display this device is, and its room.
func (s *Server) GetDisplay(ctx context.Context, _ GetDisplayRequestObject) (GetDisplayResponseObject, error) {
	r := requestFrom(ctx)
	d, err := s.Auth.AuthenticateDisplay(ctx, r.display)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthenticated) && r.display != "" {
			r.setCookie(s.clearDisplayCookie())
		}
		return nil, err
	}
	if d.Token != "" {
		r.setCookie(s.displayCookie(d.Token, d.Display.ExpiresAt))
	}
	room, err := s.Rooms.Get(ctx, d.Display.RoomID)
	if err != nil {
		return nil, err
	}
	return GetDisplay200JSONResponse{Display: toDisplay(d.Display), Room: toRoom(room)}, nil
}

// LeaveDisplay unpairs this device.
func (s *Server) LeaveDisplay(ctx context.Context, _ LeaveDisplayRequestObject) (LeaveDisplayResponseObject, error) {
	r := requestFrom(ctx)
	if err := s.Auth.UnpairDisplayToken(ctx, r.display); err != nil {
		return nil, err
	}
	r.setCookie(s.clearDisplayCookie())
	return LeaveDisplay204Response{}, nil
}

// ListDisplays lists a room's displays.
func (s *Server) ListDisplays(ctx context.Context, req ListDisplaysRequestObject) (ListDisplaysResponseObject, error) {
	if _, err := s.Rooms.Get(ctx, req.RoomId); err != nil {
		return nil, err
	}
	ds, err := s.Auth.Displays(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	out := make(ListDisplays200JSONResponse, len(ds))
	for i, d := range ds {
		out[i] = toDisplay(d)
	}
	return out, nil
}

// PairDisplay pairs the display showing a code with the room.
func (s *Server) PairDisplay(ctx context.Context, req PairDisplayRequestObject) (PairDisplayResponseObject, error) {
	if _, err := s.Rooms.Get(ctx, req.RoomId); err != nil {
		return nil, err
	}
	name := ""
	if req.Body.Name != nil {
		name = *req.Body.Name
	}
	audio := req.Body.Audio != nil && *req.Body.Audio
	d, err := s.Auth.PairDisplay(ctx, sessionFrom(ctx).User, req.RoomId, req.Body.Code, name, audio)
	if err != nil {
		return nil, err
	}
	return PairDisplay201JSONResponse(toDisplay(d)), nil
}

// manageableDisplay returns one of a room's displays, if the caller may
// change it: the room's owner, whoever paired it, or an admin.
func (s *Server) manageableDisplay(ctx context.Context, roomID, displayID string) (store.Display, error) {
	u := sessionFrom(ctx).User
	room, err := s.Rooms.Get(ctx, roomID)
	if err != nil {
		return store.Display{}, err
	}
	d, err := s.Auth.Display(ctx, displayID)
	if err != nil {
		return store.Display{}, err
	}
	if d.RoomID != room.ID {
		return store.Display{}, auth.ErrNotFound
	}
	if u.Role != store.RoleAdmin && room.OwnerID != u.ID && d.PairedBy.String != u.ID {
		return store.Display{}, auth.ErrForbidden
	}
	return d, nil
}

// UpdateDisplay turns a display's audio on or off. Off, it stops being the
// speaker if it was.
func (s *Server) UpdateDisplay(ctx context.Context, req UpdateDisplayRequestObject) (UpdateDisplayResponseObject, error) {
	d, err := s.manageableDisplay(ctx, req.RoomId, req.DisplayId)
	if err != nil {
		return nil, err
	}
	if d, err = s.Auth.SetDisplayAudio(ctx, d.ID, req.Body.Audio); err != nil {
		return nil, err
	}
	if !d.Audio {
		if err := s.Playback.Drop(ctx, d.RoomID, d.ID); err != nil {
			return nil, err
		}
	}
	return UpdateDisplay200JSONResponse(toDisplay(d)), nil
}

// UnpairDisplay removes a display: the room's owner, whoever paired it,
// or an admin may. If it was the speaker, it stops.
func (s *Server) UnpairDisplay(ctx context.Context, req UnpairDisplayRequestObject) (UnpairDisplayResponseObject, error) {
	d, err := s.manageableDisplay(ctx, req.RoomId, req.DisplayId)
	if err != nil {
		return nil, err
	}
	if err := s.Auth.UnpairDisplay(ctx, d.ID); err != nil {
		return nil, err
	}
	if err := s.Playback.Drop(ctx, d.RoomID, d.ID); err != nil {
		return nil, err
	}
	return UnpairDisplay204Response{}, nil
}

// --- Reactions ----------------------------------------------------------------

// reactionEmoji are the reactions a room can send: ReactionEmoji in the
// spec. A fixed set, since they're shown big on a shared screen.
var reactionEmoji = map[ReactionEmoji]bool{
	"🔥": true, "❤️": true, "🙌": true, "😂": true, "💃": true, "🎉": true, "😮": true, "👏": true,
}

// Reactions per person: a burst, refilling a few a second.
const (
	reactionBurst = 8
	reactionEvery = 300 * time.Millisecond
)

// reactionLimiter is a token bucket per user.
type reactionLimiter struct {
	mu      sync.Mutex
	buckets map[string]*reactionBucket
}

type reactionBucket struct {
	tokens float64
	at     time.Time
}

// allow takes a token from userID's bucket, reporting how long to wait if
// there's none.
func (l *reactionLimiter) allow(userID string, now time.Time) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = map[string]*reactionBucket{}
	}
	b := l.buckets[userID]
	if b == nil {
		if len(l.buckets) > 10_000 {
			clear(l.buckets)
		}
		b = &reactionBucket{tokens: reactionBurst, at: now}
		l.buckets[userID] = b
	}
	b.tokens = min(reactionBurst, b.tokens+float64(now.Sub(b.at))/float64(reactionEvery))
	b.at = now
	if b.tokens < 1 {
		return time.Duration((1 - b.tokens) * float64(reactionEvery)), false
	}
	b.tokens--
	return 0, true
}

// SendReaction floats an emoji up the room's big screen.
func (s *Server) SendReaction(ctx context.Context, req SendReactionRequestObject) (SendReactionResponseObject, error) {
	if !reactionEmoji[req.Body.Emoji] {
		return nil, &auth.InvalidInputError{Field: "emoji", Message: "isn't one of the room's reactions"}
	}
	if _, err := s.Rooms.Get(ctx, req.RoomId); err != nil {
		return nil, err
	}
	u := sessionFrom(ctx).User
	now := time.Now()
	if wait, ok := s.reactions.allow(u.ID, now); !ok {
		return nil, &auth.RateLimitError{RetryAfter: wait}
	}
	r := Reaction{Id: store.NewID(), RoomId: req.RoomId, UserId: u.ID, Emoji: req.Body.Emoji, At: now.UTC()}
	s.Bus.Publish(realtime.RoomTopic(req.RoomId), realtime.Event{Type: realtime.ReactionSent, Data: r})
	return SendReaction202JSONResponse(r), nil
}
