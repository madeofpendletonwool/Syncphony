// SPDX-License-Identifier: AGPL-3.0-only

package provider

import "time"

// TrackRef identifies a playable track anywhere in Syncphony: which
// provider, through whose link, and the provider's own ID.
type TrackRef struct {
	Provider string
	LinkID   string
	ID       string
}

// Track is normalized track metadata. The queue stores a copy with the
// TrackRef so it still renders when the provider is offline.
type Track struct {
	Ref      TrackRef
	Title    string
	Artists  []ArtistCredit
	Album    AlbumCredit
	Duration time.Duration
	// ISRC is set when the provider has the ISRC capability and knows it.
	ISRC     string
	Explicit bool
	Artwork  ArtworkRef
}

// ArtistCredit names an artist on a track or album. ID is the provider's
// artist ID within the same link, and may be empty.
type ArtistCredit struct {
	ID   string
	Name string
}

// AlbumCredit names the album a track is on.
type AlbumCredit struct {
	ID    string
	Title string
}

// Album, Artist and Playlist IDs are only meaningful to the session that
// returned them. Unlike tracks they are browsed, not queued, so they don't
// need a full ref.

// Album is normalized album metadata.
type Album struct {
	ID         string
	Title      string
	Artists    []ArtistCredit
	Year       int
	TrackCount int
	Artwork    ArtworkRef
}

// Artist is normalized artist metadata.
type Artist struct {
	ID      string
	Name    string
	Artwork ArtworkRef
}

// Playlist is normalized playlist metadata.
type Playlist struct {
	ID         string
	Name       string
	Owner      string
	TrackCount int
	Artwork    ArtworkRef
}

// ArtworkRef is a provider-defined handle to an image, passed back to
// Session.Artwork. Empty means no artwork.
type ArtworkRef string

// Lyrics for a track. Synced is empty when only plain lyrics are known.
type Lyrics struct {
	Plain  string
	Synced []LyricLine
}

// LyricLine is one time-synced line.
type LyricLine struct {
	At   time.Duration
	Text string
}

// SearchQuery is a search request.
type SearchQuery struct {
	Text string
	// Kinds to search. Empty means all kinds the provider supports.
	Kinds []EntityKind
	// Limit is the maximum results per kind. 0 means the provider's default.
	Limit int
	// Cursor is "" for the first page, then the previous page's Next.
	Cursor string
}

// SearchPage holds search results, grouped by kind.
type SearchPage struct {
	Tracks    []Track
	Albums    []Album
	Artists   []Artist
	Playlists []Playlist
	// Next is the cursor for the next page, or "" if this is the last.
	Next string
}

// Page is one page of a listing.
type Page[T any] struct {
	Items []T
	// Next is the cursor for the next page, or "" if this is the last.
	Next string
}
