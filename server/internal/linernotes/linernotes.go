// SPDX-License-Identifier: AGPL-3.0-only

// Package linernotes writes the liner notes for a song: its release and
// credits from MusicBrainz, a few facts (a cover? what it samples, and
// what samples it), and the artist's bio from Wikipedia, found through
// MusicBrainz's link to Wikidata. Results, misses included, are cached in
// the database.
package linernotes

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// fetchTimeout bounds writing one song's notes. MusicBrainz's rate limit
// makes it a few seconds at best, more while it's resolving the queue.
const fetchTimeout = 45 * time.Second

// Notes are a song's liner notes.
type Notes struct {
	Title         string `json:"title"`
	RecordingMBID string `json:"recordingMbid"`
	// Year the recording first came out. 0 if unknown.
	Year    int      `json:"year,omitempty"`
	Release *Release `json:"release,omitempty"`
	Artist  *Artist  `json:"artist,omitempty"`
	Credits []Credit `json:"credits"`
	Facts   []Fact   `json:"facts"`
	// CoverOf is the song this one covers, if it's a cover. Samples and
	// SampledBy are the songs it samples and those that sample it. The
	// facts say the same in words; these are for trivia (package quiz).
	CoverOf   *Work     `json:"coverOf,omitempty"`
	Samples   []SongRef `json:"samples,omitempty"`
	SampledBy []SongRef `json:"sampledBy,omitempty"`
}

// Work is a song as written, with its writers.
type Work struct {
	Title   string   `json:"title"`
	Writers []string `json:"writers,omitempty"`
}

// SongRef names another recording.
type SongRef struct {
	Title  string `json:"title"`
	Artist string `json:"artist,omitempty"`
}

// Release is where the song came out.
type Release struct {
	Title string `json:"title"`
	// Type is "Album", "Single", "EP"... or empty.
	Type   string   `json:"type,omitempty"`
	Date   string   `json:"date,omitempty"`
	Labels []string `json:"labels"`
}

// Artist is the song's main artist.
type Artist struct {
	MBID string `json:"mbid"`
	Name string `json:"name"`
	// About is a one-liner: "Group from Seattle, formed 1987".
	About string `json:"about,omitempty"`
	// Bio is a short summary from Wikipedia, and BioURL the article.
	Bio    string `json:"bio,omitempty"`
	BioURL string `json:"bioUrl,omitempty"`
}

// Credit is everyone in one role: "Produced by", "Guitar".
type Credit struct {
	Role  string   `json:"role"`
	Names []string `json:"names"`
}

// Fact kinds.
const (
	FactCover     = "cover"
	FactLive      = "live"
	FactSamples   = "samples"
	FactSampledBy = "sampled_by"
	FactOrigin    = "origin"
	FactReissue   = "first_released"
)

// Fact is one fun fact.
type Fact struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Options configure a Service.
type Options struct {
	// WikipediaURL is the Wikipedia bios come from; its language is the
	// bios'. Empty turns bios off.
	WikipediaURL string
	// WikidataURL is Wikidata. Default DefaultWikidataURL.
	WikidataURL string
	UserAgent   string
	Client      *http.Client
	// FoundTTL is how long notes are kept. Default 30 days.
	FoundTTL time.Duration
	// MissTTL is how long a miss, or notes missing a part because a
	// service was down, are kept. Default 1 day.
	MissTTL time.Duration
	Now     func() time.Time
}

// Service writes liner notes. It's safe for concurrent use.
type Service struct {
	db    *store.Store
	mb    *musicbrainz.Service
	wiki  *wikipedia // nil without Wikipedia
	opts  Options
	group singleflight.Group
}

// New returns a Service that finds songs through mb.
func New(db *store.Store, mb *musicbrainz.Service, opts Options) *Service {
	if opts.WikidataURL == "" {
		opts.WikidataURL = DefaultWikidataURL
	}
	if opts.Client == nil {
		opts.Client = http.DefaultClient
	}
	if opts.FoundTTL <= 0 {
		opts.FoundTTL = 30 * 24 * time.Hour
	}
	if opts.MissTTL <= 0 {
		opts.MissTTL = 24 * time.Hour
	}
	if opts.Now == nil {
		opts.Now = store.Now
	}
	s := &Service{db: db, mb: mb, opts: opts}
	if opts.WikipediaURL != "" {
		base := strings.TrimRight(opts.WikipediaURL, "/")
		s.wiki = &wikipedia{
			base: base, wikidata: strings.TrimRight(opts.WikidataURL, "/"), site: siteKey(base),
			userAgent: opts.UserAgent, http: opts.Client,
		}
	}
	return s
}

// Get returns t's liner notes, or provider.ErrNotFound if MusicBrainz
// doesn't know it.
func (s *Service) Get(ctx context.Context, t provider.Track) (Notes, error) {
	key := t.Ref.Provider + "\x00" + t.Ref.ID
	v, err, _ := s.group.Do(key, func() (any, error) {
		// Shared by everyone waiting, so not cut short by the first to ask.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fetchTimeout)
		defer cancel()
		return s.get(ctx, t)
	})
	if err != nil {
		return Notes{}, err
	}
	n := v.(*Notes)
	if n == nil {
		return Notes{}, fmt.Errorf("liner notes for %s %q: %w", t.Ref.Provider, t.Ref.ID, provider.ErrNotFound)
	}
	return *n, nil
}

// get returns cached notes or writes them. Nil notes are a miss.
func (s *Service) get(ctx context.Context, t provider.Track) (*Notes, error) {
	now := s.opts.Now()
	row, err := s.db.GetCachedLinerNotes(ctx, store.GetCachedLinerNotesParams{Provider: t.Ref.Provider, TrackID: t.Ref.ID, Now: now})
	switch {
	case err == nil:
		if !row.Found {
			return nil, nil
		}
		var n Notes
		if err := json.Unmarshal([]byte(row.Notes), &n); err != nil {
			return nil, fmt.Errorf("cached liner notes for %s %q: %w", t.Ref.Provider, t.Ref.ID, err)
		}
		return &n, nil
	case !store.IsNotFound(err):
		return nil, err
	}

	ids, err := s.mb.Resolve(ctx, t)
	if errors.Is(err, provider.ErrNotFound) {
		// The MusicBrainz match is cached as a miss already.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d, err := s.mb.Details(ctx, ids)
	if errors.Is(err, provider.ErrNotFound) {
		s.put(ctx, t.Ref, nil, false, now)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	n := write(ids, d)
	sure := true
	if d.Artist != nil && s.wiki != nil {
		sum, err := s.wiki.artistSummary(ctx, d.Artist.WikidataID, d.Artist.WikipediaURL)
		switch {
		case err == nil:
			n.Artist.Bio, n.Artist.BioURL = sum.Text, sum.URL
		case errors.Is(err, errNoArticle):
		default:
			// Show what we have, and try for the bio again soon.
			slog.Debug("liner notes: artist bio", "artist", d.Artist.MBID, "err", err)
			sure = false
		}
	}
	s.put(ctx, t.Ref, &n, sure, now)
	return &n, nil
}

func (s *Service) put(ctx context.Context, ref provider.TrackRef, n *Notes, sure bool, now time.Time) {
	data := []byte("{}")
	if n != nil {
		var err error
		if data, err = json.Marshal(n); err != nil {
			slog.Warn("liner notes: encoding", "err", err)
			return
		}
	}
	ttl := s.opts.MissTTL
	if n != nil && sure {
		ttl = s.opts.FoundTTL
	}
	if err := s.db.PutCachedLinerNotes(ctx, store.PutCachedLinerNotesParams{
		Provider: ref.Provider, TrackID: ref.ID, Found: n != nil, Notes: string(data), FetchedAt: now, ExpiresAt: now.Add(ttl),
	}); err != nil {
		slog.Warn("caching liner notes", "provider", ref.Provider, "track", ref.ID, "err", err)
	}
}

// Sweep deletes expired cache entries.
func (s *Service) Sweep(ctx context.Context) error {
	now := s.opts.Now()
	return errors.Join(s.db.DeleteExpiredLinerNotes(ctx, now), s.db.DeleteExpiredPageNotes(ctx, now))
}

// --- Writing the notes -------------------------------------------------------

// Caps, so the notes stay notes.
const (
	maxNamesPerRole = 6
	maxSampleFacts  = 3
	maxWriters      = 3
)

// write turns what MusicBrainz knows into notes.
func write(ids musicbrainz.IDs, d musicbrainz.Details) Notes {
	n := Notes{Title: d.Title, RecordingMBID: ids.Recording, Credits: credits(d), Facts: []Fact{}}
	first := d.FirstReleased
	if d.Release != nil {
		n.Release = &Release{Title: d.Release.Title, Type: d.Release.Type, Date: d.Release.Date, Labels: append([]string{}, d.Release.Labels...)}
		first = earliest(first, d.Release.FirstReleased)
	}
	n.Year = year(first)

	for _, w := range d.Works {
		switch {
		case slices.Contains(w.Attributes, "cover"):
			text := "A cover of “" + w.Title + "”"
			if names := writerNames(w.Writers); len(names) > 0 {
				text += ", written by " + list(names)
			}
			n.Facts = append(n.Facts, Fact{Kind: FactCover, Text: text})
			if n.CoverOf == nil {
				n.CoverOf = &Work{Title: w.Title, Writers: writerNames(w.Writers)}
			}
		case slices.Contains(w.Attributes, "live"):
			n.Facts = append(n.Facts, Fact{Kind: FactLive, Text: "A live recording of “" + w.Title + "”"})
		}
	}
	for _, r := range d.Samples[:min(len(d.Samples), maxSampleFacts)] {
		n.Facts = append(n.Facts, Fact{Kind: FactSamples, Text: "Samples " + ref(r)})
		n.Samples = append(n.Samples, SongRef{Title: r.Title, Artist: r.Artist})
	}
	for _, r := range d.SampledBy[:min(len(d.SampledBy), maxSampleFacts)] {
		n.Facts = append(n.Facts, Fact{Kind: FactSampledBy, Text: "Sampled in " + ref(r)})
		n.SampledBy = append(n.SampledBy, SongRef{Title: r.Title, Artist: r.Artist})
	}
	// On a compilation or a reissue, when it first came out.
	if d.Release != nil && n.Year > 0 && year(d.Release.Date) > n.Year {
		n.Facts = append(n.Facts, Fact{Kind: FactReissue, Text: "First released in " + strconv.Itoa(n.Year)})
	}
	if d.Artist != nil {
		n.Artist = &Artist{MBID: d.Artist.MBID, Name: d.Artist.Name, About: about(d.Artist)}
		if f := origin(d.Artist); f != "" {
			n.Facts = append(n.Facts, Fact{Kind: FactOrigin, Text: f})
		}
	}
	return n
}

func ref(r musicbrainz.RecordingRef) string {
	s := "“" + r.Title + "”"
	if r.Artist != "" {
		s += " by " + r.Artist
	}
	return s
}

// credits groups the recording's and its works' credits by role.
func credits(d musicbrainz.Details) []Credit {
	type role struct {
		rank int
		name string
	}
	byRole := map[role][]string{}
	add := func(r role, name string) {
		if name != "" && !slices.Contains(byRole[r], name) && len(byRole[r]) < maxNamesPerRole {
			byRole[r] = append(byRole[r], name)
		}
	}

	// Writers: one "Written by" unless music and lyrics are by different people.
	var music, lyrics, both []string
	for _, w := range d.Works {
		for _, c := range w.Writers {
			switch c.Type {
			case "composer":
				music = append(music, c.Name)
			case "lyricist", "librettist":
				lyrics = append(lyrics, c.Name)
			case "writer":
				both = append(both, c.Name)
			}
		}
	}
	slices.Sort(music)
	slices.Sort(lyrics)
	if len(lyrics) == 0 || slices.Equal(slices.Compact(music), slices.Compact(lyrics)) {
		for _, n := range slices.Concat(both, music, lyrics) {
			add(role{0, "Written by"}, n)
		}
	} else {
		for _, n := range slices.Concat(both, music) {
			add(role{1, "Music by"}, n)
		}
		for _, n := range lyrics {
			add(role{2, "Lyrics by"}, n)
		}
	}

	for _, c := range d.Credits {
		switch c.Type {
		case "producer":
			add(role{3, "Produced by"}, c.Name)
		case "vocal":
			add(role{4, "Vocals"}, c.Name)
		case "instrument":
			if len(c.Attributes) > 0 {
				add(role{5, capitalize(c.Attributes[0])}, c.Name)
			}
		case "performer", "performing orchestra", "conductor":
			add(role{6, capitalize(strings.ReplaceAll(c.Type, "performing ", ""))}, c.Name)
		case "arranger", "instrument arranger", "vocal arranger":
			add(role{7, "Arranged by"}, c.Name)
		case "mix":
			add(role{8, "Mixed by"}, c.Name)
		case "engineer", "recording", "audio":
			add(role{9, "Engineered by"}, c.Name)
		case "mastering":
			add(role{10, "Mastered by"}, c.Name)
		}
	}

	roles := make([]role, 0, len(byRole))
	for r := range byRole {
		roles = append(roles, r)
	}
	slices.SortFunc(roles, func(a, b role) int { return cmp.Or(cmp.Compare(a.rank, b.rank), cmp.Compare(a.name, b.name)) })
	out := make([]Credit, len(roles))
	for i, r := range roles {
		out[i] = Credit{Role: r.name, Names: byRole[r]}
	}
	return out
}

func writerNames(cs []musicbrainz.Credit) []string {
	var out []string
	for _, c := range cs {
		if !slices.Contains(out, c.Name) {
			out = append(out, c.Name)
		}
	}
	return out[:min(len(out), maxWriters)]
}

// about is a one-liner about an artist: "Group from Seattle".
func about(a *musicbrainz.Artist) string {
	kind := a.Type
	if kind == "" || kind == "Other" || kind == "Character" {
		kind = "Artist"
	}
	parts := []string{kind}
	if a.Area != "" {
		parts = append(parts, "from "+a.Area)
	}
	s := strings.Join(parts, " ")
	if a.Disambiguation != "" {
		s += " · " + a.Disambiguation
	}
	if s == "Artist" {
		return ""
	}
	return s
}

// origin is a fact about where and when an artist began.
func origin(a *musicbrainz.Artist) string {
	y := year(a.Begin)
	if y == 0 {
		return ""
	}
	var verb string
	switch a.Type {
	case "Person":
		verb = "born"
	case "Group", "Orchestra", "Choir":
		verb = "formed"
	default:
		return ""
	}
	where := cmp.Or(a.BeginArea, a.Area)
	s := a.Name + " " + verb
	if where != "" {
		s += " in " + where
	}
	return s + " in " + strconv.Itoa(y)
}

// year is a MusicBrainz date's year, or 0.
func year(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return y
}

// earliest of two MusicBrainz dates; empty ones don't count.
func earliest(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	case b[:min(4, len(b))] < a[:min(4, len(a))]:
		return b
	}
	return a
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// list joins names: "A", "A and B", "A, B and C".
func list(names []string) string {
	if len(names) <= 1 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
