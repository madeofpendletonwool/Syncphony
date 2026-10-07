// SPDX-License-Identifier: AGPL-3.0-only

package dj

import (
	"cmp"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/stats"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// How the room's taste is read.
const (
	// halfLife is how long it takes a play to count half as much.
	halfLife = 2 * time.Hour
	// profileReach is how many of the room's plays the profile reads.
	profileReach = 100
	// recentReach is how many of the room's last songs count as recent,
	// for spacing: songs of this session, since the room last went quiet
	// for stats.SessionGap.
	recentReach = 8
	// tasteFloor drops artists the room liked so long ago, against its
	// favorite, that they're no longer tonight's taste. Past nights are the
	// long-term taste's (LongTerm).
	tasteFloor = 0.01
)

// When the room turns an artist away.
const (
	// avoidBelow is the decayed net signal under which an artist is
	// turned away: about one skip in the last two hours outweighing the
	// room's liking. A skip long ago, decayed to almost nothing, doesn't.
	avoidBelow = -0.25
)

// Following a change of vibe.
const (
	// vibeWindow is how many of the members' latest songs are compared
	// with the rest of the night, once vibeMin more came before them.
	vibeWindow = 10
	vibeMin    = 3
	// vibeShiftBelow is the cosine similarity of their artists and tags to
	// the rest of the night's under which the vibe has changed. The latest songs then
	// count vibeBoost times as much, until the night settles on the new
	// vibe.
	vibeShiftBelow = 0.35
	vibeBoost      = 3.0
)

// How much each voice counts in the room's taste. Each member's likes add
// up to the same, however many songs they've queued, so nobody's run of
// songs takes over; members in the room count more.
const (
	presentWeight = 1.5
	awayWeight    = 1.0
	// roomWeight is autopilot's songs the room let play through.
	roomWeight = 0.5
)

// Input is what the room has been doing.
type Input struct {
	// History is the room's plays, newest first.
	History []store.ListHistoryRow
	// Upcoming are the songs playing and waiting.
	Upcoming []store.QueueItem
	// Mine are autopilot's recent songs, newest first, removed ones too.
	Mine []store.QueueItem
	// Present are the members in the room.
	Present []string
	// Hearts are how many hearts songs in History got, by item ID.
	Hearts map[string]int
	// Related, if set, reports whether two artists (ArtistKey) are alike,
	// so a member queueing an artist like autopilot's song counts for it.
	Related func(a, b string) bool
	// Tags, if set, are artists' tags by ArtistKey, to notice a change of
	// vibe across artists.
	Tags map[string][]musicgraph.Tag
	Now  time.Time
}

// Taste is how the room feels about one artist.
type Taste struct {
	Artist musicgraph.ArtistRef
	// Weight is how much the room likes them, from 0 to 1 against the
	// artist it likes most.
	Weight float64
	// Plays is how many of their songs members chose or let play through.
	Plays int
	// ByMember is each member's share of the liking; "" is autopilot's
	// songs the room let play.
	ByMember map[string]float64
	// Seed is the newest member song of theirs the room liked, for the
	// credit on a song it leads to. Its ID is "" if only autopilot played
	// them.
	Seed store.QueueItem
}

// Fan is the member who likes the artist most, or "" if none does.
func (t *Taste) Fan() string {
	var who string
	best := 0.0
	for u, w := range t.ByMember {
		if u != "" && (w > best || (w == best && u < who)) {
			who, best = u, w
		}
	}
	return who
}

// Profile is the room's taste: the artists it likes, how much, and who
// likes them; the artists it turned away; and what it just heard.
type Profile struct {
	// Artists the room likes, by ArtistKey.
	Artists map[string]*Taste
	// Avoid are artists the room skipped more than it liked lately, by
	// ArtistKey.
	Avoid map[string]bool
	// Doubt is how close to turning an artist away the room is, from 0 to
	// 1, for artists it skipped about as much as it liked.
	Doubt map[string]float64
	// Shifted is the vibe changing: the members' latest songs are unlike
	// the night's, and count more until it settles.
	Shifted bool
	// ThrowbacksSkipped is how many of autopilot's throwbacks the room
	// skipped or removed lately.
	ThrowbacksSkipped int
	// Recent are the artists of the room's last songs, newest first.
	Recent []string
	// RecentAlbums are the simplified albums of the room's last songs.
	RecentAlbums map[string]bool
	// Heard are the songs the room heard or has waiting, by SongKey: the
	// same song in another version counts as heard.
	Heard map[string]bool
	// RecentSongs are the members' songs the room liked lately, newest
	// first, for songs like them.
	RecentSongs []store.QueueItem
}

// ArtistKey is how artists are compared: the simplified name.
func ArtistKey(name string) string { return match.Simplify(name) }

// SongKey is how songs are compared: artist and title, without
// qualifiers, so a remaster or a live take of a song is the same song.
func SongKey(artist, title string) string {
	t, _ := match.Title(title)
	return ArtistKey(artist) + "\x00" + t
}

// TrackOf returns the track an item snapshotted when it was queued.
func TrackOf(it store.QueueItem) provider.Track {
	var t provider.Track
	_ = json.Unmarshal([]byte(it.Metadata), &t)
	return t
}

func artistOf(t provider.Track) string {
	if len(t.Artists) == 0 {
		return ""
	}
	return t.Artists[0].Name
}

// NewProfile reads the room's taste from what it's been doing. When the
// members' latest songs are unlike the rest of the night, everything
// since counts more, so the DJ follows a change of vibe instead of
// averaging it away.
func NewProfile(in Input) Profile {
	rs := reactions(in, profileReach)
	var boost time.Time
	if songs, since := memberSongs(in); len(songs) >= vibeWindow+vibeMin {
		before := slices.DeleteFunc(slices.Clone(rs), func(r reaction) bool { return !r.at.Before(since) })
		if cosine(vibe(in, songs[:vibeWindow]), tastes(in, before, time.Time{}).vibe(in.Tags)) < vibeShiftBelow {
			boost = since
		}
	}
	p := tastes(in, rs, boost)
	p.Shifted = !boost.IsZero()
	p.heard(in)
	return p
}

// tastes adds up the room's reactions into the artists it likes and turns
// away, those since boost (if not zero) counting vibeBoost times as much.
func tastes(in Input, rs []reaction, boost time.Time) Profile {
	p := Profile{Artists: map[string]*Taste{}, Avoid: map[string]bool{}, Doubt: map[string]float64{}}
	// likes[who][artist] are positive signals, decayed; net[artist] adds
	// the negative ones too.
	likes := map[string]map[string]float64{}
	net := map[string]float64{}
	for _, r := range rs {
		name, a := r.name, r.artist
		if a == "" {
			continue
		}
		v := r.v * decay(in.Now.Sub(r.at))
		if !boost.IsZero() && !r.at.Before(boost) {
			v *= vibeBoost
		}
		net[a] += v
		if r.throwback && r.v < 0 {
			p.ThrowbacksSkipped++
		}
		if v <= 0 {
			continue
		}
		if likes[r.who] == nil {
			likes[r.who] = map[string]float64{}
		}
		likes[r.who][a] += v
		taste := p.taste(a, name)
		if r.chose {
			taste.Plays++
			if taste.Seed.ID == "" {
				taste.Seed = r.item
			}
		}
	}

	present := map[string]bool{}
	for _, u := range in.Present {
		present[u] = true
	}
	most := 0.0
	for who, as := range likes {
		total := 0.0
		for _, v := range as {
			total += v
		}
		w := awayWeight
		switch {
		case who == "":
			w = roomWeight
		case present[who]:
			w = presentWeight
		}
		for a, v := range as {
			share := w * v / total
			t := p.Artists[a]
			t.Weight += share
			t.ByMember[who] += share
			most = max(most, t.Weight)
		}
	}
	for a, t := range p.Artists {
		if t.Weight /= most; net[a] < avoidBelow || t.Weight < tasteFloor {
			delete(p.Artists, a)
		}
	}
	for a, v := range net {
		switch {
		case v < avoidBelow:
			p.Avoid[a] = true
		case v < 0:
			p.Doubt[a] = v / avoidBelow
		}
	}
	return p
}

// memberSongs are the members' songs, newest first, waiting or played,
// but not skipped: the vibe they're asking for. since is when the
// vibeWindow-th newest started playing, or now if it hasn't.
func memberSongs(in Input) (songs []store.QueueItem, since time.Time) {
	seen := map[string]bool{}
	since = in.Now
	for _, it := range in.Upcoming {
		seen[it.ID] = true
		if !it.IsAutopilot() {
			songs = append(songs, it)
		}
	}
	for _, h := range in.History[:min(len(in.History), profileReach)] {
		it := h.QueueItem
		if seen[it.ID] || it.IsAutopilot() || (h.PlayHistory.EndedAt.Valid && endReason(h) != store.EndFinished) {
			continue
		}
		seen[it.ID] = true
		songs = append(songs, it)
		if len(songs) <= vibeWindow {
			since = h.PlayHistory.StartedAt
		}
	}
	return songs, since
}

// vibe is the mix of artists and tags of some songs.
func vibe(in Input, its []store.QueueItem) map[string]float64 {
	out := map[string]float64{}
	for _, it := range its {
		addVibe(out, ArtistKey(artistOf(TrackOf(it))), 1, in.Tags)
	}
	return out
}

// vibe is the mix of artists and tags of the room's taste.
func (p Profile) vibe(tags map[string][]musicgraph.Tag) map[string]float64 {
	out := map[string]float64{}
	for a, t := range p.Artists {
		addVibe(out, a, t.Weight, tags)
	}
	return out
}

func addVibe(v map[string]float64, artist string, w float64, tags map[string][]musicgraph.Tag) {
	if artist == "" {
		return
	}
	v["a\x00"+artist] += w
	for _, t := range tags[artist] {
		v["t\x00"+strings.ToLower(t.Name)] += w * t.Weight
	}
}

// cosine is the cosine similarity of two vectors, 1 if either is empty.
func cosine(a, b map[string]float64) float64 {
	var dot, na, nb float64
	for k, v := range a {
		dot += v * b[k]
		na += v * v
	}
	for _, v := range b {
		nb += v * v
	}
	if na == 0 || nb == 0 {
		return 1
	}
	return dot / math.Sqrt(na*nb)
}

// heard notes the songs the room heard or has waiting, and its last ones.
func (p *Profile) heard(in Input) {
	p.Heard, p.RecentAlbums = map[string]bool{}, map[string]bool{}
	seen := map[string]bool{}
	for _, it := range in.Upcoming {
		seen[it.ID] = true
		t := TrackOf(it)
		p.Heard[SongKey(artistOf(t), t.Title)] = true
		if it.State == store.ItemPlaying && !it.IsAutopilot() {
			p.recent(t)
		}
	}
	session, after := true, in.Now // after is when the newer play started
	for i, h := range in.History {
		it := h.QueueItem
		t := TrackOf(it)
		p.Heard[SongKey(artistOf(t), t.Title)] = true
		if seen[it.ID] || i >= profileReach {
			continue
		}
		seen[it.ID] = true
		session = session && after.Sub(endOf(h.PlayHistory)) < stats.SessionGap
		after = h.PlayHistory.StartedAt
		if session {
			p.recent(t)
		}
		if h.PlayHistory.EndedAt.Valid && endReason(h) == store.EndFinished && !it.IsAutopilot() && len(p.RecentSongs) < recentReach {
			p.RecentSongs = append(p.RecentSongs, it)
		}
	}
	for _, it := range in.Mine {
		t := TrackOf(it)
		p.Heard[SongKey(artistOf(t), t.Title)] = true
	}
}

func (p *Profile) taste(key, name string) *Taste {
	t, ok := p.Artists[key]
	if !ok {
		t = &Taste{Artist: musicgraph.ArtistRef{Name: name}, ByMember: map[string]float64{}}
		p.Artists[key] = t
	}
	return t
}

// recent notes a song among the room's last few, newest first.
func (p *Profile) recent(t provider.Track) {
	if len(p.Recent) >= recentReach {
		return
	}
	p.Recent = append(p.Recent, ArtistKey(artistOf(t)))
	if len(p.Recent) <= 5 && t.Album.Title != "" {
		p.RecentAlbums[match.Simplify(t.Album.Title)] = true
	}
}

// Top returns the artists the room likes most, most first, up to n.
func (p Profile) Top(n int) []*Taste {
	out := make([]*Taste, 0, len(p.Artists))
	for _, t := range p.Artists {
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b *Taste) int {
		if c := cmp.Compare(b.Weight, a.Weight); c != 0 {
			return c
		}
		return strings.Compare(a.Artist.Name, b.Artist.Name)
	})
	return out[:min(len(out), n)]
}

// RecentlyPlayed reports how many songs ago the room last heard an
// artist, and whether it did among its last few.
func (p Profile) RecentlyPlayed(artistKey string) (int, bool) {
	i := slices.Index(p.Recent, artistKey)
	return i, i >= 0
}

func decay(age time.Duration) float64 {
	if age <= 0 {
		return 1
	}
	return math.Pow(0.5, float64(age)/float64(halfLife))
}
