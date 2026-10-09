// SPDX-License-Identifier: AGPL-3.0-only

// Package httpapi implements the HTTP API described by api/openapi.yaml.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/admin"
	"github.com/madeofpendletonwool/syncphony/server/internal/analysis"
	"github.com/madeofpendletonwool/syncphony/server/internal/artwork"
	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/autopilot"
	"github.com/madeofpendletonwool/syncphony/server/internal/backup"
	"github.com/madeofpendletonwool/syncphony/server/internal/games"
	"github.com/madeofpendletonwool/syncphony/server/internal/linernotes"
	"github.com/madeofpendletonwool/syncphony/server/internal/links"
	"github.com/madeofpendletonwool/syncphony/server/internal/lyrics"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/nights"
	"github.com/madeofpendletonwool/syncphony/server/internal/palette"
	"github.com/madeofpendletonwool/syncphony/server/internal/playback"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/queue"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/suggest"
	"github.com/madeofpendletonwool/syncphony/server/internal/transcode"
)

// SessionCookie is the session cookie's name. It must match the "session"
// security scheme in api/openapi.yaml.
const SessionCookie = "syncphony_session"

// Server implements StrictServerInterface.
type Server struct {
	Version string
	// StartedAt is when the server started, for its uptime.
	StartedAt time.Time
	// Admin holds the server's own settings, and backs it up.
	Admin *admin.Service
	// Backups makes and restores database backups.
	Backups *backup.Service
	Auth    *auth.Service
	Links   *links.Service
	Lyrics  *lyrics.Service
	// LinerNotes writes songs' liner notes. Nil when MusicBrainz is off.
	LinerNotes *linernotes.Service
	// Graph is what's known about music, for artist and genre pages.
	// Nil when no source is on.
	Graph *musicgraph.Service
	// Artwork picks queued songs' covers.
	Artwork *artwork.Service
	// Palettes works out queued songs' artwork colors.
	Palettes *palette.Service
	// BeatMaps works out queued songs' beat maps; nil without ffmpeg.
	BeatMaps *analysis.Service
	// Realtime: room state, the event bus, and who's connected.
	Rooms    *rooms.Service
	Queue    *queue.Service
	Playback *playback.Engine
	// Nights hearts songs and crowns each night's song of the night.
	Nights *nights.Service
	// Games runs the rooms' party games. Nil turns them off.
	Games *games.Engine
	// Suggest finds songs to keep a room's vibe going.
	Suggest *suggest.Service
	// Autopilot keeps rooms' music going; admins can see what its DJ has
	// learned of a room's taste. Optional.
	Autopilot *autopilot.Service
	Bus       realtime.Bus
	Presence  *realtime.Presence
	// PingEvery is how often room sockets are pinged and their session
	// re-checked. Default 30s.
	PingEvery time.Duration
	// BaseURL is the public URL of the web app. Mutating requests must come
	// from its origin, and session cookies are Secure when it's HTTPS.
	BaseURL string
	// TrustedProxies are the reverse proxies whose X-Forwarded-For is
	// believed, for rate limiting by client IP.
	TrustedProxies []netip.Prefix

	reactions reactionLimiter
	playlists playlistCache
}

var _ StrictServerInterface = (*Server)(nil)

// Handler returns the API mounted under /api.
func (s *Server) Handler() http.Handler {
	strict := NewStrictHandlerWithOptions(s, []StrictMiddlewareFunc{s.authenticate}, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: writeError,
	})
	h := HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseURL:          "/api",
		BaseRouter:       http.NewServeMux(),
		ErrorHandlerFunc: badRequest,
	})
	// CSRF: reject cross-origin browser requests that change state. The
	// cookie is also SameSite=Lax; this covers same-site, cross-origin pages.
	cop := http.NewCrossOriginProtection()
	if err := cop.AddTrustedOrigin(origin(s.BaseURL)); err != nil {
		slog.Warn("can't trust base URL origin for CSRF checks", "base_url", s.BaseURL, "err", err)
	}
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONError(w, http.StatusForbidden, "cross_origin", "cross-origin request blocked")
	}))
	return cop.Handler(h)
}

func origin(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	return u.Scheme + "://" + u.Host
}

// GetHealth reports liveness and the build version.
func (s *Server) GetHealth(context.Context, GetHealthRequestObject) (GetHealthResponseObject, error) {
	return GetHealth200JSONResponse{Status: HealthStatusOk, Version: s.Version}, nil
}

// --- Request context ----------------------------------------------------------

// publicOps can be called without a session. Everything else requires one.
// Keys are operation IDs as the generated code spells them (capitalized).
// TestPublicOpsMatchSpec keeps this in sync with `security: []` in the spec.
var publicOps = map[string]bool{
	"GetHealth":           true,
	"GetInvite":           true,
	"Signup":              true,
	"BeginPasskeySignup":  true,
	"FinishPasskeySignup": true,
	"Login":               true,
	"BeginPasskeyLogin":   true,
	"FinishPasskeyLogin":  true,
	"Logout":              true,
	// Someone locked out uses a reset link instead.
	"GetResetLink":       true,
	"ResetPassword":      true,
	"BeginResetPasskey":  true,
	"FinishResetPasskey": true,
	"CompleteOAuthLink":  true,
	// Displays sign in with their own cookie, which these read.
	"BeginDisplayPairing": true,
	"PollDisplayPairing":  true,
	"GetDisplay":          true,
	"LeaveDisplay":        true,
	// Guests join with a pass instead of a session.
	"GetGuestInvite": true,
	"JoinAsGuest":    true,
}

type ctxKey int

const (
	ctxRequest ctxKey = iota
	ctxSession
	ctxDisplay
)

// request is what handlers need from the HTTP request.
type request struct {
	ip, userAgent, token string
	// display and pairing are a display's cookies.
	display, pairing string
	// setCookie adds a cookie to the response.
	setCookie func(*http.Cookie)
}

func requestFrom(ctx context.Context) request {
	r, _ := ctx.Value(ctxRequest).(request)
	return r
}

// sessionFrom returns the signed-in session. Only call it from handlers
// that aren't in publicOps or displayOps.
func sessionFrom(ctx context.Context) *auth.Session {
	return ctx.Value(ctxSession).(*auth.Session)
}

// authenticate puts request details in the context and, except for public
// operations, requires a session, or for displayOps, a paired display.
func (s *Server) authenticate(f StrictHandlerFunc, operationID string) StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		info := request{ip: s.clientIP(r), userAgent: r.UserAgent(), setCookie: func(c *http.Cookie) { http.SetCookie(w, c) }}
		if c, err := r.Cookie(SessionCookie); err == nil {
			info.token = c.Value
		}
		if c, err := r.Cookie(DisplayCookie); err == nil {
			info.display = c.Value
		}
		if c, err := r.Cookie(pairingCookie); err == nil {
			info.pairing = c.Value
		}
		ctx = context.WithValue(ctx, ctxRequest, info)
		if publicOps[operationID] {
			return f(ctx, w, r, req)
		}
		// A display; a signed-in user's session wins if the device is both.
		if displayOps[operationID] && info.display != "" && (info.token == "" || s.Auth.Check(ctx, info.token) != nil) {
			ctx, err := s.authenticateDisplay(ctx, w, r, operationID, req)
			if err != nil {
				return nil, err
			}
			return f(ctx, w, r, req)
		}
		sess, err := s.Auth.Authenticate(ctx, info.token)
		if err != nil {
			if errors.Is(err, auth.ErrUnauthenticated) && info.token != "" {
				http.SetCookie(w, s.clearCookie())
			}
			return nil, err
		}
		if sess.Token != "" {
			http.SetCookie(w, s.cookie(sess))
		}
		if sess.Guest != nil {
			if err := guestAllowed(sess.Guest, operationID, req); err != nil {
				return nil, err
			}
		} else if room, ok := requestRoom(req); ok {
			if err := s.enterRoom(ctx, sess.User, room, operationID); err != nil {
				return nil, err
			}
		}
		return f(context.WithValue(ctx, ctxSession, sess), w, r, req)
	}
}

func (s *Server) secure() bool { return strings.HasPrefix(s.BaseURL, "https://") }

// cookie is the session cookie. It's Secure whenever the server is served
// over HTTPS; plain-HTTP local development would otherwise never sign in.
func (s *Server) cookie(sess *auth.Session) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // Secure is set when served over HTTPS
		Name:     SessionCookie,
		Value:    sess.Token,
		Path:     "/",
		Expires:  sess.Expires,
		HttpOnly: true,
		Secure:   s.secure(),
		SameSite: http.SameSiteLaxMode,
	}
}

func (s *Server) clearCookie() *http.Cookie {
	return &http.Cookie{ //nolint:gosec // as above
		Name:     SessionCookie,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure(),
		SameSite: http.SameSiteLaxMode,
	}
}

// clientIP is the remote address, or for requests from a trusted proxy,
// the nearest untrusted address in X-Forwarded-For.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap()
	if !s.trusted(addr) {
		return addr.String()
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		addr = hop.Unmap()
		if !s.trusted(addr) {
			break
		}
	}
	return addr.String()
}

func (s *Server) trusted(addr netip.Addr) bool {
	for _, p := range s.TrustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// --- Errors -------------------------------------------------------------------

func badRequest(w http.ResponseWriter, _ *http.Request, err error) {
	writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
}

// writeError maps service errors to responses. Unexpected errors are logged
// and reported as 500 without details.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *auth.InvalidInputError
	var invalidField *links.InvalidInputError
	var limited *auth.RateLimitError
	var invalidQueue *queue.InvalidInputError
	var invalidRoom *rooms.InvalidInputError
	var invalidPlayback *playback.InvalidInputError
	var notPlayable *queue.NotPlayableError
	var repeat *queue.RepeatError
	var guestLimit *queue.GuestLimitError
	var invalidHeart *nights.InvalidInputError
	var invalidAdmin *admin.InvalidInputError
	var invalidBackup *backup.InvalidInputError
	var invalidGame *games.InvalidInputError
	switch {
	case errors.As(err, &invalidGame):
		writeJSONError(w, http.StatusBadRequest, "invalid_input", invalidGame.Error())
	case errors.As(err, &invalidBackup):
		writeJSONError(w, http.StatusBadRequest, "invalid_input", invalidBackup.Error())
	case errors.As(err, &invalidAdmin):
		writeJSONError(w, http.StatusBadRequest, "invalid_input", invalidAdmin.Error())
	case errors.As(err, &guestLimit):
		writeJSONError(w, http.StatusConflict, "guest_limit", guestLimit.Error())
	case errors.As(err, &invalidHeart):
		writeJSONError(w, http.StatusBadRequest, "invalid_input", invalidHeart.Error())
	case errors.Is(err, queue.ErrCantBorrow):
		writeJSONError(w, http.StatusForbidden, "cant_borrow", err.Error())
	case errors.As(err, &repeat):
		writeJSONError(w, http.StatusConflict, "repeat", repeat.Error())
	case errors.As(err, &notPlayable):
		writeJSONError(w, http.StatusUnprocessableEntity, "not_playable", notPlayable.Error())
	case errors.As(err, &invalidRoom):
		writeJSONError(w, http.StatusBadRequest, "invalid_input", invalidRoom.Error())
	case errors.As(err, &invalidPlayback):
		writeJSONError(w, http.StatusBadRequest, "invalid_input", invalidPlayback.Error())
	case errors.As(err, &invalidQueue):
		writeJSONError(w, http.StatusBadRequest, "invalid_input", invalidQueue.Error())
	case errors.As(err, &invalid):
		writeJSONError(w, http.StatusBadRequest, "invalid_input", invalid.Error())
	case errors.As(err, &invalidField):
		writeJSONError(w, http.StatusBadRequest, "invalid_input", invalidField.Error())
	case errors.As(err, &limited):
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(limited.RetryAfter.Seconds()))))
		writeJSONError(w, http.StatusTooManyRequests, "rate_limited", limited.Error())
	default:
		for _, e := range errorCodes {
			if errors.Is(err, e.err) {
				writeJSONError(w, e.status, e.code, e.err.Error())
				return
			}
		}
		slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal", "something went wrong")
	}
}

var errorCodes = []struct {
	err    error
	status int
	code   string
}{
	{auth.ErrInvalidCredentials, http.StatusUnauthorized, "invalid_credentials"},
	{auth.ErrUnauthenticated, http.StatusUnauthorized, "unauthenticated"},
	{auth.ErrForbidden, http.StatusForbidden, "forbidden"},
	{auth.ErrWrongPassword, http.StatusForbidden, "wrong_password"},
	{auth.ErrNotFound, http.StatusNotFound, "not_found"},
	{backup.ErrNotFound, http.StatusNotFound, "not_found"},
	{store.ErrNotDatabase, http.StatusBadRequest, "not_a_backup"},
	{auth.ErrAccountDisabled, http.StatusForbidden, "account_disabled"},
	{auth.ErrLastAdmin, http.StatusConflict, "last_admin"},
	{auth.ErrInviteInvalid, http.StatusNotFound, "invite_invalid"},
	{auth.ErrResetLinkInvalid, http.StatusNotFound, "reset_link_invalid"},
	{auth.ErrUsernameTaken, http.StatusConflict, "username_taken"},
	{auth.ErrLastCredential, http.StatusConflict, "last_credential"},
	{auth.ErrCeremonyExpired, http.StatusBadRequest, "ceremony_expired"},
	{auth.ErrPasskeyFailed, http.StatusBadRequest, "passkey_failed"},
	{auth.ErrPairingInvalid, http.StatusNotFound, "pairing_invalid"},
	{auth.ErrPairingExpired, http.StatusGone, "pairing_expired"},
	{auth.ErrTooManyPairings, http.StatusServiceUnavailable, "too_many_pairings"},
	{auth.ErrGuestPassInvalid, http.StatusNotFound, "guest_pass_invalid"},
	{auth.ErrPassFull, http.StatusConflict, "pass_full"},
	{ErrGuestsOff, http.StatusForbidden, "guests_off"},
	{ErrDisplayNoAudio, http.StatusForbidden, "forbidden"},
	{ErrDisplayOrphan, http.StatusForbidden, "forbidden"},
	{nights.ErrNotFound, http.StatusNotFound, "not_found"},
	{nights.ErrForbidden, http.StatusForbidden, "forbidden"},
	{nights.ErrNotHost, http.StatusForbidden, "forbidden"},
	{nights.ErrNotTonight, http.StatusConflict, "not_tonight"},
	{nights.ErrNothingPlayed, http.StatusConflict, "nothing_played"},
	{games.ErrNoRound, http.StatusConflict, "round_closed"},
	{games.ErrRoundRunning, http.StatusConflict, "round_running"},
	{games.ErrForbidden, http.StatusForbidden, "forbidden"},
	{games.ErrGamesOff, http.StatusConflict, "games_off"},
	{games.ErrGuestsCantPlay, http.StatusForbidden, "forbidden"},
	{games.ErrNothingPlaying, http.StatusConflict, "nothing_playing"},
	{games.ErrNoQuestion, http.StatusConflict, "no_question"},
	{ErrHiddenForRound, http.StatusConflict, "hidden_for_round"},

	{links.ErrUnknownProvider, http.StatusNotFound, "unknown_provider"},
	{links.ErrNotFound, http.StatusNotFound, "not_found"},
	{links.ErrWrongMethod, http.StatusBadRequest, "wrong_link_method"},
	{links.ErrDifferentAccount, http.StatusConflict, "different_account"},
	{links.ErrNotShareable, http.StatusBadRequest, "not_shareable"},
	{links.ErrOAuthState, http.StatusBadRequest, "oauth_state"},
	{links.ErrPairingExpired, http.StatusGone, "pairing_expired"},
	{links.ErrNotPaired, http.StatusConflict, "not_paired"},
	{rooms.ErrNotFound, http.StatusNotFound, "not_found"},
	{rooms.ErrForbidden, http.StatusForbidden, "forbidden"},
	{rooms.ErrInviteInvalid, http.StatusNotFound, "room_invite_invalid"},
	{rooms.ErrNotMember, http.StatusNotFound, "not_member"},
	{playback.ErrForbidden, http.StatusForbidden, "forbidden"},
	{playback.ErrNoPlayer, http.StatusConflict, "no_player"},
	{playback.ErrNotPlayer, http.StatusConflict, "not_player"},
	{playback.ErrNothingPlaying, http.StatusConflict, "nothing_playing"},
	{playback.ErrNotStreamable, http.StatusConflict, "not_streamable"},
	{transcode.ErrNoTranscoder, http.StatusUnsupportedMediaType, "unsupported_format"},
	{analysis.ErrUnavailable, http.StatusNotFound, "no_beat_map"},
	{provider.ErrRange, http.StatusRequestedRangeNotSatisfiable, "range_not_satisfiable"},
	{queue.ErrNotFound, http.StatusNotFound, "not_found"},
	{queue.ErrForbidden, http.StatusForbidden, "forbidden"},
	{queue.ErrNotQueued, http.StatusConflict, "not_queued"},
	{queue.ErrUndoExpired, http.StatusConflict, "undo_expired"},
	{suggest.ErrScope, http.StatusBadRequest, "invalid_input"},
	{suggest.ErrOrigin, http.StatusBadRequest, "invalid_input"},
	// Errors from a service, while linking or using a link.
	{provider.ErrInvalidCredentials, http.StatusBadRequest, "service_rejected_credentials"},
	{provider.ErrAuthExpired, http.StatusConflict, "needs_relink"},
	{provider.ErrUnavailable, http.StatusBadGateway, "service_unavailable"},
	{provider.ErrRateLimited, http.StatusServiceUnavailable, "service_rate_limited"},
	{provider.ErrNotFound, http.StatusNotFound, "not_found"},
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Error{Code: code, Message: message})
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// nonZero returns nil for v's zero value.
func nonZero[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}

// nonEmpty returns nil for "".
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// timePtr returns nil for the zero time.
func timePtr(t time.Time, valid bool) *time.Time {
	if !valid {
		return nil
	}
	return &t
}
