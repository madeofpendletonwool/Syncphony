// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"

	"github.com/madeofpendletonwool/syncphony/server/internal/dj"
	"github.com/madeofpendletonwool/syncphony/server/internal/suggest"
)

// GetSuggestions returns songs to keep a room's vibe going: like your own
// songs or like everyone's, from what's played through or what's queued.
func (s *Server) GetSuggestions(ctx context.Context, req GetSuggestionsRequestObject) (GetSuggestionsResponseObject, error) {
	q := suggest.Query{RoomID: req.RoomId, UserID: sessionFrom(ctx).User.ID, Scope: suggest.ScopeMine, Origin: suggest.OriginHistory, Limit: 20}
	if req.Params.Scope != nil {
		q.Scope = suggest.Scope(*req.Params.Scope)
	}
	if req.Params.Source != nil {
		q.Origin = suggest.Origin(*req.Params.Source)
	}
	if req.Params.Limit != nil {
		q.Limit = *req.Params.Limit
	}
	if req.Params.Refresh != nil {
		q.Refresh = *req.Params.Refresh
	}
	ss, err := s.Suggest.Suggest(ctx, q)
	if err != nil {
		return nil, err
	}
	out := Suggestions{Scope: SuggestionsScope(q.Scope), Items: make([]Suggestion, len(ss))}
	for i, sg := range ss {
		seed := dj.TrackOf(sg.Seed)
		because := SuggestionSeed{ItemId: sg.Seed.ID, Title: seed.Title, UserId: sg.Seed.AddedBy}
		if len(seed.Artists) > 0 {
			because.Artist = ptr(seed.Artists[0].Name)
		}
		out.Items[i] = Suggestion{Track: toTrackResult(sg.Track), Because: because}
	}
	return GetSuggestions200JSONResponse(out), nil
}
