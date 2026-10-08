// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/playback"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
)

// GetPlayback returns what a room is playing.
func (s *Server) GetPlayback(ctx context.Context, req GetPlaybackRequestObject) (GetPlaybackResponseObject, error) {
	np, err := s.Playback.NowPlaying(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	return GetPlayback200JSONResponse(toNowPlaying(np)), nil
}

// ControlPlayback plays, pauses, skips, seeks, or plays a queued song now.
func (s *Server) ControlPlayback(ctx context.Context, req ControlPlaybackRequestObject) (ControlPlaybackResponseObject, error) {
	c := playback.Command{Action: string(req.Body.Action)}
	if req.Body.PositionMs != nil {
		c.Position = time.Duration(*req.Body.PositionMs) * time.Millisecond
	}
	if req.Body.ItemId != nil {
		c.ItemID = *req.Body.ItemId
	}
	userID := ""
	if d := displayFrom(ctx); d != nil {
		// A screen playing the room has a speaker's buttons, as whoever paired it.
		if c.Action != playback.ActionPlay && c.Action != playback.ActionPause && c.Action != playback.ActionSkip {
			return nil, playback.ErrForbidden
		}
		id, _, err := speakerOf(ctx, "")
		if err != nil {
			return nil, err
		}
		userID = id
	} else {
		userID = sessionFrom(ctx).User.ID
	}
	np, err := s.Playback.Command(ctx, req.RoomId, userID, c)
	if err != nil {
		return nil, err
	}
	return ControlPlayback200JSONResponse(toNowPlaying(np)), nil
}

// ClaimPlayer makes the caller's device the room's speaker. A display
// claims as itself, under its own name.
func (s *Server) ClaimPlayer(ctx context.Context, req ClaimPlayerRequestObject) (ClaimPlayerResponseObject, error) {
	userID, device, err := speakerOf(ctx, req.Body.DeviceId)
	if err != nil {
		return nil, err
	}
	name := req.Body.Name
	if d := displayFrom(ctx); d != nil {
		name = d.Name
	}
	itemID := ""
	if req.Body.PlayItemId != nil {
		itemID = *req.Body.PlayItemId
	}
	np, err := s.Playback.ClaimAndPlay(ctx, req.RoomId, userID, device, name, itemID)
	if err != nil {
		return nil, err
	}
	return ClaimPlayer200JSONResponse(toNowPlaying(np)), nil
}

// ReleasePlayer stops a device being the room's speaker.
func (s *Server) ReleasePlayer(ctx context.Context, req ReleasePlayerRequestObject) (ReleasePlayerResponseObject, error) {
	userID, device, err := speakerOf(ctx, req.Params.DeviceId)
	if err != nil {
		return nil, err
	}
	np, err := s.Playback.Release(ctx, req.RoomId, userID, device)
	if err != nil {
		return nil, err
	}
	return ReleasePlayer200JSONResponse(toNowPlaying(np)), nil
}

// ReportPlayback takes the speaker's report on the song it's streaming.
func (s *Server) ReportPlayback(ctx context.Context, req ReportPlaybackRequestObject) (ReportPlaybackResponseObject, error) {
	userID, device, err := speakerOf(ctx, req.Body.DeviceId)
	if err != nil {
		return nil, err
	}
	rep := playback.Report{
		DeviceID: device, ItemID: req.Body.ItemId, Event: string(req.Body.Event),
		Position: time.Duration(req.Body.PositionMs) * time.Millisecond,
	}
	if req.Body.Error != nil {
		rep.Error = *req.Body.Error
	}
	np, err := s.Playback.Report(ctx, req.RoomId, userID, rep)
	if err != nil {
		return nil, err
	}
	return ReportPlayback200JSONResponse(toNowPlaying(np)), nil
}

// StreamQueueItem proxies a song's audio from its service.
func (s *Server) StreamQueueItem(ctx context.Context, req StreamQueueItemRequestObject) (StreamQueueItemResponseObject, error) {
	var opts provider.StreamOpts
	if req.Params.Range != nil {
		opts.Range = parseRange(*req.Params.Range)
	}
	if req.Params.Accept != nil {
		for t := range strings.SplitSeq(*req.Params.Accept, ",") {
			if t = strings.TrimSpace(t); t != "" {
				opts.Accept = append(opts.Accept, t)
			}
		}
	}
	if req.Params.MaxBitrate != nil && *req.Params.MaxBitrate > 0 {
		opts.MaxBitrate = *req.Params.MaxBitrate
	}
	if req.Params.Start != nil && *req.Params.Start > 0 {
		opts.Start = time.Duration(*req.Params.Start) * time.Millisecond
	}
	a, err := s.Playback.Stream(ctx, req.RoomId, req.ItemId, opts)
	if err != nil {
		return nil, err
	}
	return streamResponse{a}, nil
}

// parseRange reads a single "bytes=start-[end]" range. Anything else
// (suffix ranges, several ranges) means the whole file.
func parseRange(h string) *provider.ByteRange {
	spec, ok := strings.CutPrefix(strings.TrimSpace(h), "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return nil
	}
	from, to, ok := strings.Cut(spec, "-")
	start, err := strconv.ParseInt(strings.TrimSpace(from), 10, 64)
	if !ok || err != nil || start < 0 {
		return nil
	}
	r := &provider.ByteRange{Start: start, End: -1}
	if to = strings.TrimSpace(to); to != "" {
		end, err := strconv.ParseInt(to, 10, 64)
		if err != nil || end < start {
			return nil
		}
		r.End = end
	}
	return r
}

// streamResponse writes an audio stream, as 206 when it's part of the file.
type streamResponse struct{ a *provider.AudioStream }

func (r streamResponse) VisitStreamQueueItemResponse(w http.ResponseWriter) error {
	defer r.a.Body.Close()
	h := w.Header()
	h.Set("Content-Type", r.a.ContentType)
	// The stream is the room's, through someone's link: never cache it in shared caches.
	h.Set("Cache-Control", "private, no-store")
	if r.a.Seekable {
		h.Set("Accept-Ranges", "bytes")
	} else {
		h.Set("Accept-Ranges", "none")
	}
	if r.a.Length >= 0 {
		h.Set("Content-Length", fmt.Sprint(r.a.Length))
	}
	status := http.StatusOK
	if cr := r.a.ContentRange(); cr != "" {
		h.Set("Content-Range", cr)
		status = http.StatusPartialContent
	}
	w.WriteHeader(status)
	if _, err := io.Copy(w, r.a.Body); err != nil {
		// Usually the player hung up (seeking, skipping); nothing to send.
		slog.Debug("stream ended early", "err", err)
	}
	return nil
}

func toNowPlaying(np rooms.NowPlaying) NowPlaying {
	out := NowPlaying{
		RoomId: np.RoomID, State: PlaybackState(np.State), PositionMs: np.Position.Milliseconds(), At: np.At, Revision: np.Revision,
	}
	if np.Item != nil {
		out.Item = ptr(toQueueItem(*np.Item))
	}
	if np.Next != nil {
		out.Next = ptr(toQueueItem(*np.Next))
	}
	if np.Driver != "" {
		out.Driver = ptr(NowPlayingDriver(np.Driver))
	}
	if p := np.Player; p != nil {
		out.Player = &Player{DeviceId: p.DeviceID, UserId: p.UserID, Name: p.Name, LastSeen: p.LastSeen}
	}
	if v := np.SkipVotes; v != nil {
		out.SkipVotes = &SkipVotes{Voters: v.Voters, Needed: v.Needed}
	}
	if v := np.PlayNow; v != nil {
		out.PlayNow = &PlayNowVote{ItemId: v.ItemID, By: v.By, Voters: v.Voters, Needed: v.Needed, Expires: v.Expires}
		if v.Back {
			out.PlayNow.Back = ptr(true)
		}
		if v.Item != nil {
			out.PlayNow.Item = ptr(toQueueItem(*v.Item))
		}
	}
	return out
}
