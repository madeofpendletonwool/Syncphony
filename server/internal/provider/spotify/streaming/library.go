// SPDX-License-Identifier: AGPL-3.0-only

package streaming

import (
	"cmp"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	golibrespot "github.com/devgianlu/go-librespot"
	collectionpb "github.com/devgianlu/go-librespot/proto/spotify/collection/v2"
	extmetadatapb "github.com/devgianlu/go-librespot/proto/spotify/extendedmetadata"
	metadatapb "github.com/devgianlu/go-librespot/proto/spotify/metadata"
	playlist4pb "github.com/devgianlu/go-librespot/proto/spotify/playlist4"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/spotify"
)

// Playlists come from Spotify's playlist service, Liked Songs from its
// collection service, what was played lately from its recently played
// service, and track metadata from its extended metadata service, all
// through spclient with the account's own login, as the Spotify apps list a
// library.

var _ spotify.Library = (*Backend)(nil)

const (
	// libraryTimeout bounds a library call. spclient retries transient
	// failures with backoff for up to 15 minutes, which nobody waits for.
	libraryTimeout = 30 * time.Second
	// rootlistPage is how many library entries are asked for per request,
	// and maxRootlistPages caps the requests for one listing.
	rootlistPage     = 500
	maxRootlistPages = 20
	// rootlistDecorations has the playlist service inline each playlist's
	// name, picture, length and owner, so one request lists the library.
	rootlistDecorations = "revision,attributes,length,owner,capabilities"
	// maxMetadataBatch is the most tracks asked about per metadata request.
	maxMetadataBatch = 100
	// imageCDN serves images by file ID.
	imageCDN = "https://i.scdn.co/image/"
	// likedSongsImage is the picture Spotify gives Liked Songs.
	likedSongsImage = "https://misc.scdn.co/liked-songs/liked-songs-640.png"
	// recentlyPlayedLimit is how many recently played things are asked
	// for. Of the playlists among them that aren't in the library, the
	// first maxPlayedPlaylists are asked about and listed.
	recentlyPlayedLimit = 50
	maxPlayedPlaylists  = 20
	// maxLookups caps the playlists asked about one by one per listing.
	maxLookups = 40
	// The collection service (Liked Songs) is read collectionPage items a
	// request, for at most maxCollectionPages requests.
	collectionContentType = "application/vnd.collection-v2.spotify.proto"
	collectionPage        = 2000
	maxCollectionPages    = 10
)

// Playlists implements spotify.Library. The library is required; what was
// played lately and Liked Songs are best effort, so a hiccup in either
// still lists the library.
func (b *Backend) Playlists(ctx context.Context, login spotify.Login) ([]spotify.LibraryPlaylist, error) {
	ctx, cancel := context.WithTimeout(ctx, libraryTimeout)
	defer cancel()
	c, err := b.conn(ctx, login)
	if err != nil {
		return nil, err
	}
	lib, err := b.rootlist(ctx, c, login.Username)
	if err != nil {
		return nil, err
	}
	recent, err := b.recentlyPlayed(ctx, c, login.Username)
	if err != nil {
		b.log.WithError(err).Warnf("spotify streaming: recently played; listing the library in its own order")
	}
	// The library lists some of Spotify's mixes with no length, whether or
	// not they have any items, so those are asked about along with the
	// playlists only played.
	var ask []string
	for _, p := range lib {
		if p.TrackCount == 0 {
			ask = append(ask, p.ID)
		}
	}
	played := unsavedPlaylists(lib, recent)
	found := b.lookupPlaylists(ctx, c, append(ask, played...))
	all, likedAs := mergePlaylists(lib, played, found)

	liked, err := b.likedSongs(ctx, c, login.Username)
	if err != nil {
		b.log.WithError(err).Warnf("spotify streaming: liked songs; leaving them out")
	} else if len(liked) > 0 {
		all = append(all, spotify.LibraryPlaylist{
			ID: spotify.LikedSongsID, Name: "Liked Songs", Owner: login.Username, TrackCount: len(liked),
			Images: []spotify.Image{{URL: likedSongsImage, Width: 640}},
		})
	}
	return orderPlaylists(all, recent, likedAs), nil
}

// rootlist lists the playlists saved in the account's library.
func (b *Backend) rootlist(ctx context.Context, c *conn, username string) ([]spotify.LibraryPlaylist, error) {
	var items []*playlist4pb.Item
	var metas []*playlist4pb.MetaItem
	path := "/playlist/v2/user/" + url.PathEscape(username) + "/rootlist"
	for range maxRootlistPages {
		q := url.Values{
			"decorate": {rootlistDecorations},
			"from":     {strconv.Itoa(len(items))},
			"length":   {strconv.Itoa(rootlistPage)},
		}
		list, err := b.list(ctx, c, "library", path, q)
		if err != nil {
			return nil, err
		}
		contents := list.GetContents()
		if len(contents.GetMetaItems()) != len(contents.GetItems()) {
			return nil, fmt.Errorf("spotify streaming: library page has %d items but %d meta items: %w",
				len(contents.GetItems()), len(contents.GetMetaItems()), provider.ErrUnavailable)
		}
		items = append(items, contents.GetItems()...)
		metas = append(metas, contents.GetMetaItems()...)
		if !contents.GetTruncated() || len(contents.GetItems()) == 0 {
			break
		}
	}
	return libraryPlaylists(items, metas), nil
}

// played is something the account played: a playlist, album, artist or
// Liked Songs, by URI.
type played struct {
	URI  string `json:"uri"`
	Time int64  `json:"lastPlayedTime"` // Unix milliseconds
}

// recentlyPlayed returns what the account played lately, most recent
// first, as the Spotify apps' "Recently played" shows it.
func (b *Backend) recentlyPlayed(ctx context.Context, c *conn, username string) ([]played, error) {
	q := url.Values{"format": {"json"}, "offset": {"0"}, "limit": {strconv.Itoa(recentlyPlayedLimit)}, "filter": {"default"}}
	resp, err := c.sp.RequestOnce(ctx, http.MethodGet, "/recently-played/v3/user/"+url.PathEscape(username)+"/recently-played", q, nil, nil)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("spotify streaming: recently played: %w: %w", provider.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError("recently played", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	var body struct {
		PlayContexts []played `json:"playContexts"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("spotify streaming: recently played: malformed response: %w", provider.ErrUnavailable)
	}
	return body.PlayContexts, nil
}

// lookedUp is a playlist asked about directly.
type lookedUp struct {
	playlist spotify.LibraryPlaylist
	// likedSongs marks the playlist Spotify plays Liked Songs as.
	likedSongs bool
}

// lookupPlaylists asks about playlists by ID, a few at a time. Ones that
// can't be read (private, or deleted) are left out of the result.
func (b *Backend) lookupPlaylists(ctx context.Context, c *conn, ids []string) map[string]lookedUp {
	ids = ids[:min(len(ids), maxLookups)]
	found := make([]*lookedUp, len(ids))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, id := range ids {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			list, err := b.list(ctx, c, "playlist "+id, "/playlist/v2/playlist/"+id, url.Values{"from": {"0"}, "length": {"0"}})
			if err != nil {
				return
			}
			attrs := list.GetAttributes()
			found[i] = &lookedUp{
				playlist: spotify.LibraryPlaylist{
					ID: id, Name: attrs.GetName(), Owner: list.GetOwnerUsername(),
					TrackCount: int(list.GetLength()), Images: playlistImages(attrs),
				},
				likedSongs: attrs.GetFormat() == "liked-songs",
			}
		})
	}
	wg.Wait()
	out := map[string]lookedUp{}
	for _, f := range found {
		if f != nil {
			out[f.playlist.ID] = *f
		}
	}
	return out
}

// unsavedPlaylists returns the IDs of up to maxPlayedPlaylists playlists
// in recent that aren't in lib, in recent's order.
func unsavedPlaylists(lib []spotify.LibraryPlaylist, recent []played) []string {
	seen := map[string]bool{}
	for _, p := range lib {
		seen[p.ID] = true
	}
	var ids []string
	for _, p := range recent {
		id, ok := strings.CutPrefix(p.URI, "spotify:playlist:")
		if ok && !seen[id] && len(ids) < maxPlayedPlaylists {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// mergePlaylists lists the library's playlists and the ones only played
// (by ID), using what lookupPlaylists found. Playlists found empty are
// left out (Spotify's DJ is one), as are played ones it couldn't find.
// The playlist Spotify plays Liked Songs as is left out too, and its ID
// returned as likedAs.
func mergePlaylists(lib []spotify.LibraryPlaylist, played []string, found map[string]lookedUp) (out []spotify.LibraryPlaylist, likedAs string) {
	keep := func(p spotify.LibraryPlaylist) {
		f, ok := found[p.ID]
		switch {
		case ok && f.likedSongs:
			likedAs = p.ID
		case ok && (f.playlist.TrackCount == 0 || f.playlist.Name == ""):
		case ok:
			p.TrackCount = f.playlist.TrackCount
			out = append(out, p)
		default:
			out = append(out, p)
		}
	}
	for _, p := range lib {
		keep(p)
	}
	for _, id := range played {
		if f, ok := found[id]; ok {
			keep(f.playlist)
		}
	}
	return out, likedAs
}

// orderPlaylists sorts playlists most recently played first. Liked Songs
// is played as the playlist likedAs (or, from older apps, as the user's
// collection); never played, it goes first, as Spotify pins it. Other
// playlists never played keep their order, after the rest.
func orderPlaylists(all []spotify.LibraryPlaylist, recent []played, likedAs string) []spotify.LibraryPlaylist {
	at := map[string]int64{}
	for _, p := range recent {
		id, ok := strings.CutPrefix(p.URI, "spotify:playlist:")
		if (ok && id == likedAs) || strings.HasSuffix(p.URI, ":collection") {
			id, ok = spotify.LikedSongsID, true
		}
		if ok {
			at[id] = max(at[id], p.Time)
		}
	}
	if at[spotify.LikedSongsID] == 0 {
		at[spotify.LikedSongsID] = math.MaxInt64
	}
	out := slices.Clone(all)
	slices.SortStableFunc(out, func(a, b spotify.LibraryPlaylist) int { return cmp.Compare(at[b.ID], at[a.ID]) })
	return out
}

// libraryPlaylists turns library entries into playlists. Folders are
// marked by spotify:start-group and spotify:end-group entries around their
// contents, and are dropped. items and metas are parallel.
func libraryPlaylists(items []*playlist4pb.Item, metas []*playlist4pb.MetaItem) []spotify.LibraryPlaylist {
	var out []spotify.LibraryPlaylist
	for i, it := range items {
		id, ok := strings.CutPrefix(it.GetUri(), "spotify:playlist:")
		if !ok || i >= len(metas) {
			continue
		}
		m := metas[i]
		attrs := m.GetAttributes()
		// A playlist its owner deleted stays in followers' libraries, nameless.
		if attrs.GetName() == "" || attrs.GetDeletedByOwner() {
			continue
		}
		out = append(out, spotify.LibraryPlaylist{
			ID:         id,
			Name:       attrs.GetName(),
			Owner:      m.GetOwnerUsername(),
			TrackCount: int(m.GetLength()),
			Images:     playlistImages(attrs),
		})
	}
	return out
}

// pictureWidths are the widths of a playlist picture's named sizes.
var pictureWidths = map[string]int{"small": 60, "default": 300, "large": 640, "xlarge": 1000}

// playlistImages returns a playlist's picture: Spotify's own mixes name a
// URL per size; a picture the owner uploaded is an image ID. Playlists
// without either show a mosaic of their first albums, which isn't stored
// anywhere, so they have no artwork here.
func playlistImages(attrs *playlist4pb.ListAttributes) []spotify.Image {
	var out []spotify.Image
	for _, s := range attrs.GetPictureSize() {
		if s.GetUrl() != "" {
			out = append(out, spotify.Image{URL: s.GetUrl(), Width: pictureWidths[s.GetTargetName()]})
		}
	}
	if len(out) == 0 && len(attrs.GetPicture()) > 0 {
		out = append(out, spotify.Image{URL: imageCDN + hex.EncodeToString(attrs.GetPicture())})
	}
	return out
}

// PlaylistTracks implements spotify.Library.
func (b *Backend) PlaylistTracks(ctx context.Context, login spotify.Login, playlistID string, from, n int) (spotify.LibraryPage, error) {
	if playlistID == spotify.LikedSongsID {
		return b.likedTracks(ctx, login, from, n)
	}
	id, err := golibrespot.SpotifyIdFromBase62(golibrespot.SpotifyIdTypePlaylist, playlistID)
	if err != nil {
		return spotify.LibraryPage{}, fmt.Errorf("spotify streaming: playlist %q: %w", playlistID, provider.ErrNotFound)
	}
	ctx, cancel := context.WithTimeout(ctx, libraryTimeout)
	defer cancel()
	c, err := b.conn(ctx, login)
	if err != nil {
		return spotify.LibraryPage{}, err
	}
	q := url.Values{"from": {strconv.Itoa(from)}, "length": {strconv.Itoa(n)}}
	list, err := b.list(ctx, c, "playlist "+playlistID, "/playlist/v2/playlist/"+id.Base62(), q)
	if err != nil {
		return spotify.LibraryPage{}, err
	}
	var uris []string
	for _, it := range list.GetContents().GetItems() {
		uris = append(uris, it.GetUri())
	}
	tracks, err := b.tracks(ctx, c, uris)
	if err != nil {
		return spotify.LibraryPage{}, err
	}
	return spotify.LibraryPage{Tracks: tracks, Total: int(list.GetLength())}, nil
}

// likedTracks lists a page of Liked Songs, newest first.
func (b *Backend) likedTracks(ctx context.Context, login spotify.Login, from, n int) (spotify.LibraryPage, error) {
	ctx, cancel := context.WithTimeout(ctx, libraryTimeout)
	defer cancel()
	c, err := b.conn(ctx, login)
	if err != nil {
		return spotify.LibraryPage{}, err
	}
	liked, err := b.likedSongs(ctx, c, login.Username)
	if err != nil {
		return spotify.LibraryPage{}, err
	}
	tracks, err := b.tracks(ctx, c, liked[min(from, len(liked)):min(from+n, len(liked))])
	if err != nil {
		return spotify.LibraryPage{}, err
	}
	return spotify.LibraryPage{Tracks: tracks, Total: len(liked)}, nil
}

// tracks returns the tracks among uris that Spotify has metadata for, in
// order. URIs of anything but tracks are skipped.
func (b *Backend) tracks(ctx context.Context, c *conn, uris []string) ([]spotify.LibraryTrack, error) {
	var want []string
	for _, uri := range uris {
		if strings.HasPrefix(uri, "spotify:track:") {
			want = append(want, uri)
		}
	}
	meta := map[string]*metadatapb.Track{}
	for len(want) > 0 {
		batch := want[:min(len(want), maxMetadataBatch)]
		want = want[len(batch):]
		if err := b.trackMetadata(ctx, c, batch, meta); err != nil {
			return nil, err
		}
	}
	var out []spotify.LibraryTrack
	for _, uri := range uris {
		if m, ok := meta[uri]; ok {
			out = append(out, libraryTrack(strings.TrimPrefix(uri, "spotify:track:"), m))
		}
	}
	return out, nil
}

// likedSongs returns the URIs of the account's Liked Songs, newest first.
// The collection service lists saved albums and the rest in the same set,
// in URI order, so the whole set is read and sorted.
func (b *Backend) likedSongs(ctx context.Context, c *conn, username string) ([]string, error) {
	var all []*collectionpb.CollectionItem
	token := ""
	for range maxCollectionPages {
		resp, err := c.sp.RequestOnce(ctx, http.MethodPost, "/collection/v2/paging", nil, http.Header{
			"Content-Type": {collectionContentType},
			"Accept":       {collectionContentType},
		}, pageRequest(username, "collection", token, collectionPage))
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, fmt.Errorf("spotify streaming: liked songs: %w: %w", provider.ErrUnavailable, err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, statusError("liked songs", resp.StatusCode, resp.Header.Get("Retry-After"))
		}
		if err != nil {
			return nil, fmt.Errorf("spotify streaming: liked songs: %w: %w", provider.ErrUnavailable, err)
		}
		items, next, err := parsePageResponse(body)
		if err != nil {
			return nil, fmt.Errorf("spotify streaming: liked songs: malformed response: %w", provider.ErrUnavailable)
		}
		all = append(all, items...)
		if next == "" {
			break
		}
		token = next
	}
	return likedURIs(all), nil
}

// likedURIs returns the tracks among a collection's items, newest first.
func likedURIs(items []*collectionpb.CollectionItem) []string {
	var tracks []*collectionpb.CollectionItem
	for _, it := range items {
		if strings.HasPrefix(it.GetUri(), "spotify:track:") && !it.GetIsRemoved() {
			tracks = append(tracks, it)
		}
	}
	slices.SortStableFunc(tracks, func(a, b *collectionpb.CollectionItem) int { return cmp.Compare(b.GetAddedAt(), a.GetAddedAt()) })
	out := make([]string, len(tracks))
	for i, it := range tracks {
		out[i] = it.GetUri()
	}
	return out
}

// pageRequest encodes a collection service PageRequest, which go-librespot
// has no type for: username 1, set 2, pagination_token 3, limit 4.
func pageRequest(username, set, token string, limit uint64) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendString(b, username)
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendString(b, set)
	if token != "" {
		b = protowire.AppendTag(b, 3, protowire.BytesType)
		b = protowire.AppendString(b, token)
	}
	b = protowire.AppendTag(b, 4, protowire.VarintType)
	return protowire.AppendVarint(b, limit)
}

// parsePageResponse decodes a collection service PageResponse: items 1
// (CollectionItems) and next_page_token 2. Other fields are skipped.
func parsePageResponse(b []byte) ([]*collectionpb.CollectionItem, string, error) {
	var items []*collectionpb.CollectionItem
	next := ""
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil, "", protowire.ParseError(n)
		}
		b = b[n:]
		if typ != protowire.BytesType || (num != 1 && num != 2) {
			n = protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return nil, "", protowire.ParseError(n)
			}
			b = b[n:]
			continue
		}
		v, n := protowire.ConsumeBytes(b)
		if n < 0 {
			return nil, "", protowire.ParseError(n)
		}
		b = b[n:]
		if num == 2 {
			next = string(v)
			continue
		}
		var it collectionpb.CollectionItem
		if err := proto.Unmarshal(v, &it); err != nil {
			return nil, "", err
		}
		items = append(items, &it)
	}
	return items, next, nil
}

// trackMetadata asks for the metadata of tracks by URI, adding what
// Spotify has to into. Tracks it has none for are left out.
func (b *Backend) trackMetadata(ctx context.Context, c *conn, uris []string, into map[string]*metadatapb.Track) error {
	req := &extmetadatapb.BatchedEntityRequest{}
	for _, uri := range uris {
		req.EntityRequest = append(req.EntityRequest, &extmetadatapb.EntityRequest{
			EntityUri: uri,
			Query:     []*extmetadatapb.ExtensionQuery{{ExtensionKind: extmetadatapb.ExtensionKind_TRACK_V4}},
		})
	}
	resp, err := c.sp.ExtendedMetadata(ctx, req)
	if err != nil {
		var se *golibrespot.HTTPStatusError
		if errors.As(err, &se) {
			return statusError("track metadata", se.StatusCode, "")
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("spotify streaming: track metadata: %w: %w", provider.ErrUnavailable, err)
	}
	for _, item := range resp.GetExtendedMetadata() {
		for _, d := range item.GetExtensionData() {
			if d.GetHeader().GetStatusCode() != http.StatusOK || d.GetExtensionData() == nil {
				continue
			}
			var t metadatapb.Track
			if err := d.GetExtensionData().UnmarshalTo(&t); err == nil {
				into[d.GetEntityUri()] = &t
			}
		}
	}
	return nil
}

// imageWidths are the widths of the image sizes Spotify names.
var imageWidths = map[metadatapb.Image_Size]int{
	metadatapb.Image_SMALL:   64,
	metadatapb.Image_DEFAULT: 300,
	metadatapb.Image_LARGE:   640,
	metadatapb.Image_XLARGE:  1000,
}

// libraryTrack converts a track's metadata. id is the ID the playlist
// names it by, which is what plays.
func libraryTrack(id string, t *metadatapb.Track) spotify.LibraryTrack {
	out := spotify.LibraryTrack{
		ID:       id,
		Name:     t.GetName(),
		Duration: time.Duration(t.GetDuration()) * time.Millisecond,
		Explicit: t.GetExplicit(),
	}
	for _, a := range t.GetArtist() {
		if a.GetName() != "" {
			out.Artists = append(out.Artists, provider.ArtistCredit{ID: base62(a.GetGid()), Name: a.GetName()})
		}
	}
	al := t.GetAlbum()
	out.AlbumID, out.AlbumName = base62(al.GetGid()), al.GetName()
	covers := al.GetCoverGroup().GetImage()
	if len(covers) == 0 {
		covers = al.GetCover()
	}
	for _, im := range covers {
		if len(im.GetFileId()) == 0 {
			continue
		}
		w := int(im.GetWidth())
		if w <= 0 {
			w = imageWidths[im.GetSize()]
		}
		out.AlbumCover = append(out.AlbumCover, spotify.Image{URL: imageCDN + hex.EncodeToString(im.GetFileId()), Width: w})
	}
	return out
}

// base62 converts a GID to a Spotify ID, or "" if it isn't one.
func base62(gid []byte) string {
	if len(gid) != 16 {
		return ""
	}
	return golibrespot.GidToBase62(gid)
}

// list GETs a playlist service path, once: spclient's retries could
// outlast libraryTimeout, and a playlist that doesn't exist is answered
// with a 503.
func (b *Backend) list(ctx context.Context, c *conn, what, path string, q url.Values) (*playlist4pb.SelectedListContent, error) {
	resp, err := c.sp.RequestOnce(ctx, http.MethodGet, path, q, nil, nil)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("spotify streaming: %s: %w: %w", what, provider.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(what, resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("spotify streaming: %s: %w: %w", what, provider.ErrUnavailable, err)
	}
	var list playlist4pb.SelectedListContent
	if err := proto.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("spotify streaming: %s: malformed response: %w", what, provider.ErrUnavailable)
	}
	return &list, nil
}

// statusError maps a failed spclient response.
func statusError(what string, status int, retryAfter string) error {
	switch {
	case status == http.StatusNotFound || status == http.StatusForbidden:
		// 403 is someone else's private playlist.
		return fmt.Errorf("spotify streaming: %s: HTTP %d: %w", what, status, provider.ErrNotFound)
	case status == http.StatusTooManyRequests:
		secs, _ := strconv.Atoi(strings.TrimSpace(retryAfter))
		return fmt.Errorf("spotify streaming: %s: %w", what, &provider.RateLimitError{RetryAfter: time.Duration(max(secs, 0)) * time.Second})
	case status >= 500:
		return fmt.Errorf("spotify streaming: %s: HTTP %d: %w", what, status, provider.ErrUnavailable)
	}
	return fmt.Errorf("spotify streaming: %s: HTTP %d", what, status)
}
