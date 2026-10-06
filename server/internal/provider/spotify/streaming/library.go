// SPDX-License-Identifier: AGPL-3.0-only

package streaming

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	golibrespot "github.com/devgianlu/go-librespot"
	extmetadatapb "github.com/devgianlu/go-librespot/proto/spotify/extendedmetadata"
	metadatapb "github.com/devgianlu/go-librespot/proto/spotify/metadata"
	playlist4pb "github.com/devgianlu/go-librespot/proto/spotify/playlist4"
	"google.golang.org/protobuf/proto"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/spotify"
)

// Playlists come from Spotify's playlist service and track metadata from its
// extended metadata service, both through spclient with the account's own
// login, as the Spotify apps list a library.

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
)

// Playlists implements spotify.Library.
func (b *Backend) Playlists(ctx context.Context, login spotify.Login) ([]spotify.LibraryPlaylist, error) {
	ctx, cancel := context.WithTimeout(ctx, libraryTimeout)
	defer cancel()
	c, err := b.conn(ctx, login)
	if err != nil {
		return nil, err
	}
	var items []*playlist4pb.Item
	var metas []*playlist4pb.MetaItem
	path := "/playlist/v2/user/" + url.PathEscape(login.Username) + "/rootlist"
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
		if strings.HasPrefix(it.GetUri(), "spotify:track:") {
			uris = append(uris, it.GetUri())
		}
	}
	meta := map[string]*metadatapb.Track{}
	for len(uris) > 0 {
		batch := uris[:min(len(uris), maxMetadataBatch)]
		uris = uris[len(batch):]
		if err := b.trackMetadata(ctx, c, batch, meta); err != nil {
			return spotify.LibraryPage{}, err
		}
	}
	page := spotify.LibraryPage{Total: int(list.GetLength())}
	for _, it := range list.GetContents().GetItems() {
		if m, ok := meta[it.GetUri()]; ok {
			page.Tracks = append(page.Tracks, libraryTrack(strings.TrimPrefix(it.GetUri(), "spotify:track:"), m))
		}
	}
	return page, nil
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
