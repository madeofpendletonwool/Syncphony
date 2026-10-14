// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/madeofpendletonwool/syncphony/server/internal/discover"
	"github.com/madeofpendletonwool/syncphony/server/internal/library"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Personal discovery (ADR 0017). Every call is the signed-in member's own;
// guests have no discovery.

func toDiscoverySet(set discover.Set) DiscoverySet {
	out := DiscoverySet{
		Id: set.ID, Name: set.Name, Explore: int(set.Explore), Mode: DiscoveryMode(set.Mode), FreshDays: DiscoveryFreshDays(set.FreshDays),
		Artists: make([]DiscoverySetArtist, len(set.Artists)), CreatedAt: set.CreatedAt, UpdatedAt: set.UpdatedAt,
	}
	out.GrowAuto, out.RoomWeight = set.GrowAuto != 0, DiscoveryRoomWeight(set.RoomWeight)
	if set.RoomID.Valid {
		out.RoomId = &set.RoomID.String
	}
	for i, a := range set.Artists {
		out.Artists[i] = DiscoverySetArtist{
			Key: a.ArtistKey, Name: a.Name, Mbid: nonEmpty(a.Mbid.String), AddedFrom: DiscoverySetArtistAddedFrom(a.AddedFrom), AddedAt: a.AddedAt,
		}
	}
	return out
}

func toDiscoveryLibrary(st library.Status) DiscoveryLibrary {
	return DiscoveryLibrary{Links: st.Links, Songs: st.Songs, Building: st.Building, BuiltAt: timePtr(st.BuiltAt, !st.BuiltAt.IsZero())}
}

func toDiscoveryBlock(b store.DiscoveryBlock) DiscoveryBlock {
	return DiscoveryBlock{Key: b.ArtistKey, Name: b.Name, CreatedAt: b.CreatedAt}
}

func toSeeds(in []DiscoverySeed) []discover.Seed {
	out := make([]discover.Seed, len(in))
	for i, a := range in {
		out[i] = discover.Seed{Name: a.Name, MBID: deref(a.Mbid)}
	}
	return out
}

// ListDiscoverySets lists the caller's sets.
func (s *Server) ListDiscoverySets(ctx context.Context, _ ListDiscoverySetsRequestObject) (ListDiscoverySetsResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	sets, err := s.Discover.Sets(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	out := make(ListDiscoverySets200JSONResponse, len(sets))
	for i, set := range sets {
		out[i] = DiscoverySetSummary{
			Id: set.ID, Name: set.Name, Explore: int(set.Explore), Mode: DiscoveryMode(set.Mode), FreshDays: DiscoveryFreshDays(set.FreshDays), ArtistCount: int(set.Artists),
			Wanted: int(set.Wanted), Got: int(set.Got), CreatedAt: set.CreatedAt, UpdatedAt: set.UpdatedAt,
		}
		if set.RoomID.Valid {
			out[i].RoomId = &set.RoomID.String
		}
	}
	return out, nil
}

// CreateDiscoverySet makes a set.
func (s *Server) CreateDiscoverySet(ctx context.Context, req CreateDiscoverySetRequestObject) (CreateDiscoverySetResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	var seeds []discover.Seed
	if req.Body.Artists != nil {
		seeds = toSeeds(*req.Body.Artists)
	}
	set, err := s.Discover.Create(ctx, u.ID, req.Body.Name, seeds)
	if err != nil {
		return nil, err
	}
	return CreateDiscoverySet201JSONResponse(toDiscoverySet(set)), nil
}

// GetDiscoverySet returns one of the caller's sets.
func (s *Server) GetDiscoverySet(ctx context.Context, req GetDiscoverySetRequestObject) (GetDiscoverySetResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	set, err := s.Discover.Get(ctx, u.ID, req.SetId)
	if err != nil {
		return nil, err
	}
	return GetDiscoverySet200JSONResponse(toDiscoverySet(set)), nil
}

// UpdateDiscoverySet renames a set or changes its explore or mode.
func (s *Server) UpdateDiscoverySet(ctx context.Context, req UpdateDiscoverySetRequestObject) (UpdateDiscoverySetResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	c := discover.Changes{Name: req.Body.Name, Explore: req.Body.Explore, GrowAuto: req.Body.GrowAuto, RoomID: req.Body.RoomId}
	if req.Body.RoomWeight != nil {
		c.RoomWeight = ptr(string(*req.Body.RoomWeight))
	}
	if req.Body.Mode != nil {
		c.Mode = ptr(string(*req.Body.Mode))
	}
	if req.Body.FreshDays != nil {
		c.FreshDays = ptr(int(*req.Body.FreshDays))
	}
	set, err := s.Discover.Update(ctx, u.ID, req.SetId, c)
	if err != nil {
		return nil, err
	}
	return UpdateDiscoverySet200JSONResponse(toDiscoverySet(set)), nil
}

// DeleteDiscoverySet deletes a set.
func (s *Server) DeleteDiscoverySet(ctx context.Context, req DeleteDiscoverySetRequestObject) (DeleteDiscoverySetResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Discover.Delete(ctx, u.ID, req.SetId); err != nil {
		return nil, err
	}
	return DeleteDiscoverySet204Response{}, nil
}

// AddDiscoverySetArtist adds a seed artist to a set.
func (s *Server) AddDiscoverySetArtist(ctx context.Context, req AddDiscoverySetArtistRequestObject) (AddDiscoverySetArtistResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	set, err := s.Discover.AddArtist(ctx, u.ID, req.SetId, discover.Seed{Name: req.Body.Name, MBID: deref(req.Body.Mbid)})
	if err != nil {
		return nil, err
	}
	return AddDiscoverySetArtist200JSONResponse(toDiscoverySet(set)), nil
}

// RemoveDiscoverySetArtist takes a seed artist out of a set.
func (s *Server) RemoveDiscoverySetArtist(ctx context.Context, req RemoveDiscoverySetArtistRequestObject) (RemoveDiscoverySetArtistResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	set, err := s.Discover.RemoveArtist(ctx, u.ID, req.SetId, req.ArtistKey)
	if err != nil {
		return nil, err
	}
	return RemoveDiscoverySetArtist200JSONResponse(toDiscoverySet(set)), nil
}

// GetDiscoveryFeed returns a page of a set's feed.
func (s *Server) GetDiscoveryFeed(ctx context.Context, req GetDiscoveryFeedRequestObject) (GetDiscoveryFeedResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	f, err := s.Discover.Feed(ctx, u.ID, req.SetId, deref(req.Params.Cursor), req.Params.Refresh != nil && *req.Params.Refresh)
	if err != nil {
		return nil, err
	}
	out := DiscoveryFeed{Items: toDiscoveryItems(f.Items), Next: nonEmpty(f.Next), Library: toDiscoveryLibrary(f.Library), NoGraph: f.NoGraph}
	return GetDiscoveryFeed200JSONResponse(out), nil
}

func toDiscoveryItems(items []discover.Item) []DiscoveryItem {
	out := make([]DiscoveryItem, len(items))
	for i, it := range items {
		out[i] = DiscoveryItem{
			Key: it.Key, Title: it.Title, Artist: it.Artist, Album: nonEmpty(it.Album), Mbid: nonEmpty(it.MBID), Isrc: nonEmpty(it.ISRC),
			DeezerId: nonZero(it.DeezerID), Cover: nonEmpty(it.Cover), Because: it.Because, Seed: it.Seed,
		}
		if out[i].Because == nil {
			out[i].Because = []string{}
		}
	}
	return out
}

// GetDiscoveryFresh returns a set's recent releases.
func (s *Server) GetDiscoveryFresh(ctx context.Context, req GetDiscoveryFreshRequestObject) (GetDiscoveryFreshResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	f, err := s.Discover.Fresh(ctx, u.ID, req.SetId)
	if err != nil {
		return nil, err
	}
	out := DiscoveryFresh{
		Items: make([]DiscoveryRelease, len(f.Items)), Days: DiscoveryFreshDays(f.Days), Partial: f.Partial,
		Library: toDiscoveryLibrary(f.Library), NoGraph: f.NoGraph, NoReleases: f.NoReleases,
	}
	for i, it := range f.Items {
		out.Items[i] = DiscoveryRelease{
			Key: it.Key, Title: it.Title, Artist: it.Artist, Date: it.Date, Kind: DiscoveryReleaseKind(it.Kind),
			Mbid: nonEmpty(it.MBID), CoverRelease: nonEmpty(it.CoverRelease), DeezerId: nonZero(it.DeezerID), Cover: nonEmpty(it.Cover),
			Sources: it.Sources, Because: it.Because, Seed: it.Seed,
		}
		if out.Items[i].Because == nil {
			out.Items[i].Because = []string{}
		}
		if out.Items[i].Sources == nil {
			out.Items[i].Sources = []string{}
		}
	}
	return GetDiscoveryFresh200JSONResponse(out), nil
}

// ListDiscoveryReleaseTracks lists a release's songs, with previews.
func (s *Server) ListDiscoveryReleaseTracks(ctx context.Context, req ListDiscoveryReleaseTracksRequestObject) (ListDiscoveryReleaseTracksResponseObject, error) {
	if _, err := member(ctx); err != nil {
		return nil, err
	}
	var id int64
	if req.Params.DeezerId != nil {
		id = *req.Params.DeezerId
	}
	items, err := s.Discover.ReleaseTracks(ctx, req.Params.Artist, req.Params.Title, id)
	if err != nil {
		return nil, err
	}
	return ListDiscoveryReleaseTracks200JSONResponse(DiscoveryReleaseTracks{Items: toDiscoveryItems(items)}), nil
}

// GetDiscoveryReleaseArt loads a release's cover from the Cover Art Archive.
func (s *Server) GetDiscoveryReleaseArt(ctx context.Context, req GetDiscoveryReleaseArtRequestObject) (GetDiscoveryReleaseArtResponseObject, error) {
	if _, err := member(ctx); err != nil {
		return nil, err
	}
	if s.MusicBrainz == nil {
		return nil, provider.ErrNotFound
	}
	size := 250
	if req.Params.Size != nil {
		size = *req.Params.Size
	}
	release, group := req.Mbid, ""
	if req.Params.Group != nil && *req.Params.Group {
		release, group = "", req.Mbid
	}
	img, err := s.MusicBrainz.ReleaseArt(ctx, release, group, size)
	if err != nil {
		return nil, err
	}
	return artworkResponse{body: io.NopCloser(bytes.NewReader(img.Data)), ct: img.ContentType}, nil
}

func (r artworkResponse) VisitGetDiscoveryReleaseArtResponse(w http.ResponseWriter) error {
	return r.write(w)
}

// GetDiscoveryMap returns a map of artists around an artist or a set.
func (s *Server) GetDiscoveryMap(ctx context.Context, req GetDiscoveryMapRequestObject) (GetDiscoveryMapResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	p := req.Params
	q := discover.MapQuery{Artist: deref(p.Artist), MBID: deref(p.Mbid), SetID: deref(p.SetId)}
	if p.Depth != nil {
		q.Depth = *p.Depth
	}
	if p.Limit != nil {
		q.Limit = *p.Limit
	}
	m, err := s.Discover.Map(ctx, u.ID, q)
	if err != nil {
		return nil, err
	}
	out := DiscoveryMap{
		Nodes: make([]DiscoveryMapNode, len(m.Nodes)), Edges: make([]DiscoveryMapEdge, len(m.Edges)),
		Partial: m.Partial, NoGraph: m.NoGraph, Library: toDiscoveryLibrary(m.Library),
	}
	if m.Set != nil {
		out.Set = ptr(toDiscoverySet(*m.Set))
	}
	for i, n := range m.Nodes {
		out.Nodes[i] = DiscoveryMapNode{
			Key: n.Key, Name: n.Name, Mbid: nonEmpty(n.MBID), Fans: nonZero(n.Fans), Picture: nonEmpty(n.Picture), Tags: n.Tags,
			Owned: n.Owned, Seed: n.Seed, Center: n.Center, Blocked: n.Blocked, Hop: n.Hop, Seeds: n.Seeds,
			Score: float32(n.Score), Known: n.Known,
		}
		if out.Nodes[i].Tags == nil {
			out.Nodes[i].Tags = []string{}
		}
	}
	for i, e := range m.Edges {
		out.Edges[i] = DiscoveryMapEdge{From: e.From, To: e.To, Score: float32(e.Score), Sources: e.Sources}
	}
	return GetDiscoveryMap200JSONResponse(out), nil
}

// ListDiscoveryArtistSongs returns an artist's songs the caller doesn't have.
func (s *Server) ListDiscoveryArtistSongs(ctx context.Context, req ListDiscoveryArtistSongsRequestObject) (ListDiscoveryArtistSongsResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	songs, err := s.Discover.ArtistSongs(ctx, u.ID, req.Params.Artist, deref(req.Params.Mbid))
	if err != nil {
		return nil, err
	}
	return ListDiscoveryArtistSongs200JSONResponse(DiscoverySongs{
		Items: toDiscoveryItems(songs.Items), Library: toDiscoveryLibrary(songs.Library), NoGraph: songs.NoGraph,
	}), nil
}

// PostDiscoveryFeedback records a thumb on a song.
func (s *Server) PostDiscoveryFeedback(ctx context.Context, req PostDiscoveryFeedbackRequestObject) (PostDiscoveryFeedbackResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	song := discover.Song{Title: b.Title, Artist: b.Artist, Album: deref(b.Album), Cover: deref(b.Cover), MBID: deref(b.Mbid), ISRC: deref(b.Isrc)}
	if b.DeezerId != nil {
		song.DeezerID = *b.DeezerId
	}
	did, err := s.Discover.Thumb(ctx, u.ID, deref(b.SetId), string(b.Kind), song)
	if err != nil {
		return nil, err
	}
	out := DiscoveryThumbResult{Grew: nonEmpty(did.Grew)}
	if did.Want != nil {
		out.Want = ptr(toWantItem(*did.Want))
	}
	return PostDiscoveryFeedback200JSONResponse(out), nil
}

// ListDiscoveryBlocks lists the artists the caller blocked.
func (s *Server) ListDiscoveryBlocks(ctx context.Context, _ ListDiscoveryBlocksRequestObject) (ListDiscoveryBlocksResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	bs, err := s.Discover.Blocks(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	out := make(ListDiscoveryBlocks200JSONResponse, len(bs))
	for i, b := range bs {
		out[i] = toDiscoveryBlock(b)
	}
	return out, nil
}

// BlockDiscoveryArtist keeps an artist out of the caller's feeds.
func (s *Server) BlockDiscoveryArtist(ctx context.Context, req BlockDiscoveryArtistRequestObject) (BlockDiscoveryArtistResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	b, err := s.Discover.Block(ctx, u.ID, req.Body.Name)
	if err != nil {
		return nil, err
	}
	return BlockDiscoveryArtist200JSONResponse(toDiscoveryBlock(b)), nil
}

// UnblockDiscoveryArtist lets a blocked artist back in.
func (s *Server) UnblockDiscoveryArtist(ctx context.Context, req UnblockDiscoveryArtistRequestObject) (UnblockDiscoveryArtistResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Discover.Unblock(ctx, u.ID, req.ArtistKey); err != nil {
		return nil, err
	}
	return UnblockDiscoveryArtist204Response{}, nil
}

// SearchDiscoveryArtists finds artists to seed a set with.
func (s *Server) SearchDiscoveryArtists(ctx context.Context, req SearchDiscoveryArtistsRequestObject) (SearchDiscoveryArtistsResponseObject, error) {
	if _, err := member(ctx); err != nil {
		return nil, err
	}
	found, err := s.Discover.SearchArtists(ctx, req.Params.Q)
	if err != nil {
		return nil, err
	}
	out := make(SearchDiscoveryArtists200JSONResponse, len(found))
	for i, a := range found {
		out[i] = DiscoveryArtist{Name: a.Name, Fans: nonZero(a.Fans), Picture: nonEmpty(a.Picture)}
	}
	return out, nil
}

// GetDiscoveryLibrary reports how far the caller's library is indexed,
// and starts indexing it if it never was.
func (s *Server) GetDiscoveryLibrary(ctx context.Context, _ GetDiscoveryLibraryRequestObject) (GetDiscoveryLibraryResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	st, err := s.Discover.LibraryStatus(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return GetDiscoveryLibrary200JSONResponse(toDiscoveryLibrary(st)), nil
}

// RefreshDiscoveryLibrary indexes the caller's library again, in the
// background.
func (s *Server) RefreshDiscoveryLibrary(ctx context.Context, _ RefreshDiscoveryLibraryRequestObject) (RefreshDiscoveryLibraryResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	if s.Library != nil {
		if err := s.Library.Refresh(ctx, u.ID); err != nil {
			return nil, err
		}
	}
	st, err := s.Discover.LibraryStatus(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	st.Building = st.Building || s.Library != nil
	return RefreshDiscoveryLibrary202JSONResponse(toDiscoveryLibrary(st)), nil
}

// GetDiscoveryPreview streams a song's 30-second preview from Deezer.
func (s *Server) GetDiscoveryPreview(ctx context.Context, req GetDiscoveryPreviewRequestObject) (GetDiscoveryPreviewResponseObject, error) {
	if _, err := member(ctx); err != nil {
		return nil, err
	}
	if s.Deezer == nil {
		return nil, provider.ErrNotFound
	}
	var r *provider.ByteRange
	if req.Params.Range != nil {
		r = parseRange(*req.Params.Range)
	}
	a, err := s.Deezer.OpenPreview(ctx, req.DeezerTrackId, r)
	if err != nil {
		return nil, err
	}
	return previewResponse{streamResponse{a}}, nil
}

// previewResponse writes a preview as streamResponse writes a song.
type previewResponse struct{ streamResponse }

func (r previewResponse) VisitGetDiscoveryPreviewResponse(w http.ResponseWriter) error {
	return r.VisitStreamQueueItemResponse(w)
}

// GetDiscoveryImage loads a cover or artist picture from Deezer.
func (s *Server) GetDiscoveryImage(ctx context.Context, req GetDiscoveryImageRequestObject) (GetDiscoveryImageResponseObject, error) {
	if _, err := member(ctx); err != nil {
		return nil, err
	}
	if s.Deezer == nil {
		return nil, provider.ErrNotFound
	}
	size := 250
	if req.Params.Size != nil {
		size = *req.Params.Size
	}
	img, ct, err := s.Deezer.Image(ctx, req.Kind, req.ImageId, size)
	if err != nil {
		return nil, err
	}
	return artworkResponse{body: io.NopCloser(bytes.NewReader(img)), ct: ct}, nil
}

func (r artworkResponse) VisitGetDiscoveryImageResponse(w http.ResponseWriter) error {
	return r.write(w)
}

// Stage 4 (MAD-824..826): the Want list, sets that learn, and rooms as a
// set's source.

func toWantItem(w store.WantItem) WantItem {
	out := WantItem{
		Id: w.ID, Kind: WantKind(w.Kind), Artist: w.Artist, Title: w.Title, Release: nonEmpty(w.ReleaseTitle),
		Mbid: nonEmpty(w.Mbid.String), Isrc: nonEmpty(w.Isrc.String), DeezerId: nonZero(w.DeezerID.Int64), Cover: nonEmpty(w.Cover),
		SetId: nonEmpty(w.SetID.String), Status: WantStatus(w.Status), GotAt: timePtr(w.GotAt.Time, w.GotAt.Valid), AddedAt: w.AddedAt,
	}
	if w.GotBy.Valid {
		out.GotBy = ptr(WantItemGotBy(w.GotBy.String))
	}
	return out
}

// ListDiscoveryWants returns the caller's Want list.
func (s *Server) ListDiscoveryWants(ctx context.Context, _ ListDiscoveryWantsRequestObject) (ListDiscoveryWantsResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	l, err := s.Discover.Wants(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	out := WantList{Items: make([]WantItem, len(l.Items)), Landed: l.Landed}
	for i, w := range l.Items {
		out.Items[i] = toWantItem(w)
	}
	return ListDiscoveryWants200JSONResponse(out), nil
}

// AddDiscoveryWant puts a song or release on the caller's Want list.
func (s *Server) AddDiscoveryWant(ctx context.Context, req AddDiscoveryWantRequestObject) (AddDiscoveryWantResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	w := discover.Want{
		Kind: string(b.Kind), Artist: b.Artist, Title: b.Title, Release: deref(b.Release),
		MBID: deref(b.Mbid), ISRC: deref(b.Isrc), Cover: deref(b.Cover), SetID: deref(b.SetId),
	}
	if b.DeezerId != nil {
		w.DeezerID = *b.DeezerId
	}
	it, err := s.Discover.AddWant(ctx, u.ID, w)
	if err != nil {
		return nil, err
	}
	return AddDiscoveryWant200JSONResponse(toWantItem(it)), nil
}

// UpdateDiscoveryWant marks a thing wanted, got or dismissed.
func (s *Server) UpdateDiscoveryWant(ctx context.Context, req UpdateDiscoveryWantRequestObject) (UpdateDiscoveryWantResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	it, err := s.Discover.SetWant(ctx, u.ID, req.WantId, string(req.Body.Status))
	if err != nil {
		return nil, err
	}
	return UpdateDiscoveryWant200JSONResponse(toWantItem(it)), nil
}

// SeeDiscoveryLanded notes the caller saw what landed in their library.
func (s *Server) SeeDiscoveryLanded(ctx context.Context, _ SeeDiscoveryLandedRequestObject) (SeeDiscoveryLandedResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Discover.SeeLanded(ctx, u.ID); err != nil {
		return nil, err
	}
	return SeeDiscoveryLanded204Response{}, nil
}

func toOffers(os []discover.Offer) []DiscoveryOffer {
	out := make([]DiscoveryOffer, len(os))
	for i, o := range os {
		out[i] = DiscoveryOffer{Key: o.Key, Name: o.Name, Thumbs: o.Thumbs}
	}
	return out
}

// GetDiscoverySuggestions returns what a set's thumbs suggest.
func (s *Server) GetDiscoverySuggestions(ctx context.Context, req GetDiscoverySuggestionsRequestObject) (GetDiscoverySuggestionsResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	sg, err := s.Discover.Suggestions(ctx, u.ID, req.SetId)
	if err != nil {
		return nil, err
	}
	return GetDiscoverySuggestions200JSONResponse(DiscoverySuggestions{Grow: toOffers(sg.Grow), Block: toOffers(sg.Block)}), nil
}

// DismissDiscoverySuggestion says no to a suggestion.
func (s *Server) DismissDiscoverySuggestion(ctx context.Context, req DismissDiscoverySuggestionRequestObject) (DismissDiscoverySuggestionResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Discover.Dismiss(ctx, u.ID, req.SetId, string(req.Body.Kind), req.Body.Artist); err != nil {
		return nil, err
	}
	return DismissDiscoverySuggestion204Response{}, nil
}

// ImportDiscoverySetArtists adds the artists the caller picked to a set.
func (s *Server) ImportDiscoverySetArtists(ctx context.Context, req ImportDiscoverySetArtistsRequestObject) (ImportDiscoverySetArtistsResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	set, err := s.Discover.Import(ctx, u.ID, req.SetId, string(req.Body.From), toSeeds(req.Body.Artists))
	if err != nil {
		return nil, err
	}
	return ImportDiscoverySetArtists200JSONResponse(toDiscoverySet(set)), nil
}

// ListDiscoveryStarred lists the artists the caller starred on Navidrome.
func (s *Server) ListDiscoveryStarred(ctx context.Context, _ ListDiscoveryStarredRequestObject) (ListDiscoveryStarredResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	as, err := s.Discover.Starred(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	out := make(ListDiscoveryStarred200JSONResponse, len(as))
	for i, a := range as {
		out[i] = DiscoveryStarredArtist{Name: a.Name, Artist: a.Artist, Albums: a.Albums, Songs: a.Songs}
	}
	return out, nil
}

// GetDiscoveryRoomTaste returns a room's favorite artists, or a night's.
func (s *Server) GetDiscoveryRoomTaste(ctx context.Context, req GetDiscoveryRoomTasteRequestObject) (GetDiscoveryRoomTasteResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	p := req.Params
	as, err := s.Discover.RoomTaste(ctx, u.ID, p.RoomId, deref(p.NightId), p.Mine != nil && *p.Mine)
	if err != nil {
		return nil, err
	}
	out := make(GetDiscoveryRoomTaste200JSONResponse, len(as))
	for i, a := range as {
		out[i] = DiscoveryTasteArtist{Name: a.Name, Weight: float32(a.Weight), Plays: a.Plays}
	}
	return out, nil
}
