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
		Id: set.ID, Name: set.Name, Explore: int(set.Explore), Mode: DiscoveryMode(set.Mode),
		Artists: make([]DiscoverySetArtist, len(set.Artists)), CreatedAt: set.CreatedAt, UpdatedAt: set.UpdatedAt,
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
			Id: set.ID, Name: set.Name, Explore: int(set.Explore), Mode: DiscoveryMode(set.Mode), ArtistCount: int(set.Artists),
			CreatedAt: set.CreatedAt, UpdatedAt: set.UpdatedAt,
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
	c := discover.Changes{Name: req.Body.Name, Explore: req.Body.Explore}
	if req.Body.Mode != nil {
		c.Mode = ptr(string(*req.Body.Mode))
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
	out := DiscoveryFeed{Items: make([]DiscoveryItem, len(f.Items)), Next: nonEmpty(f.Next), Library: toDiscoveryLibrary(f.Library), NoGraph: f.NoGraph}
	for i, it := range f.Items {
		out.Items[i] = DiscoveryItem{
			Key: it.Key, Title: it.Title, Artist: it.Artist, Album: nonEmpty(it.Album), Mbid: nonEmpty(it.MBID), Isrc: nonEmpty(it.ISRC),
			DeezerId: nonZero(it.DeezerID), Cover: nonEmpty(it.Cover), Because: it.Because, Seed: it.Seed,
		}
		if out.Items[i].Because == nil {
			out.Items[i].Because = []string{}
		}
	}
	return GetDiscoveryFeed200JSONResponse(out), nil
}

// PostDiscoveryFeedback records a thumb on a song.
func (s *Server) PostDiscoveryFeedback(ctx context.Context, req PostDiscoveryFeedbackRequestObject) (PostDiscoveryFeedbackResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	song := discover.Song{Title: b.Title, Artist: b.Artist, MBID: deref(b.Mbid), ISRC: deref(b.Isrc)}
	if b.DeezerId != nil {
		song.DeezerID = *b.DeezerId
	}
	if err := s.Discover.Thumb(ctx, u.ID, deref(b.SetId), string(b.Kind), song); err != nil {
		return nil, err
	}
	return PostDiscoveryFeedback204Response{}, nil
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
