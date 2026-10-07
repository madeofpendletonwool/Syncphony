// SPDX-License-Identifier: AGPL-3.0-only

package dj

import (
	"cmp"
	"context"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// Set flow (MAD-757): the DJ plays a set, not separate picks. Each
// candidate is scored on how well it follows the song before it (tempo,
// era, energy) and how close it comes to the room's energy curve, and on
// how well the best few songs after it would follow on. It still queues
// one song at a time and plans again on every queue change.
const (
	// planAhead is how many songs a plan reaches, the candidate included.
	planAhead = 4
	// aheadWeight is how much the flow of the rest of a candidate's plan
	// counts, against the flow into it.
	aheadWeight = 0.5
	// The flow terms' weights. Each term's fit is 0 to 1, and adds
	// weight·(fit − ½) to a score: a good fit raises it, a poor one lowers
	// it, and an unknown one leaves it alone.
	bpmWeight    = 0.12
	eraWeight    = 0.10
	energyWeight = 0.12
	curveWeight  = 0.10
	// bpmEase is how far apart two tempos may be and still mix cleanly,
	// as a fraction: about ±8%. Past bpmEase+bpmSpan they don't fit at
	// all. Half and double time count as the same tempo.
	bpmEase = 0.08
	bpmSpan = 0.17
	// The tempos the DJ believes; outside them a BPM is a tagging error.
	minBPM, maxBPM = 40, 250
	// eraEase is how many years apart two songs may be and still be the
	// same era, as are any two of the same decade; past eraEase+eraSpan
	// they don't fit at all.
	eraEase = 4
	eraSpan = 16
	// energyEase is how far the energy may move from one song to the next
	// and still be gradual; past energyEase+energySpan it's a jump.
	energyEase = 0.15
	energySpan = 0.35
	// curveSpan is how far from the curve's energy a song fits not at all.
	curveSpan = 0.4
	// flowReach is how many of the songs before the pick are read for
	// what came last: the nearest that knows a tempo, era or energy.
	flowReach = 3
	// songLength is how long a planned song is taken to play.
	songLength = 4 * time.Minute
)

// sound is what flow knows about a song. Zero BPM or year is unknown, and
// so is a negative energy.
type sound struct {
	bpm    float64
	year   int
	energy float64
}

var unknownSound = sound{energy: -1}

// soundOf reads a song's sound: tempo and year from the graph's cache,
// else from its service's tags; energy from its tempo and its artist's
// tags. A year the song doesn't give is read from tags like "80s".
func soundOf(t musicgraph.Track, bpm, year int, tags []musicgraph.Tag) sound {
	s := unknownSound
	for _, b := range []float64{t.BPM, float64(bpm)} {
		if b >= minBPM && b <= maxBPM {
			s.bpm = b
			break
		}
	}
	switch {
	case t.Year > 0:
		s.year = t.Year
	case year > 0:
		s.year = year
	default:
		s.year = decadeOf(tags)
	}
	s.energy = energyOf(s.bpm, tags)
	return s
}

// orElse fills in what s doesn't know from o.
func (s sound) orElse(o sound) sound {
	if s.bpm == 0 {
		s.bpm = o.bpm
	}
	if s.year == 0 {
		s.year = o.year
	}
	if s.energy < 0 {
		s.energy = o.energy
	}
	return s
}

// tagEnergy is how energetic a style of music is, from 0 to 1. A song's
// energy is its artist's tags' energies, weighted by tag, mixed with its
// tempo's. It's a guess: no source the DJ can use says how energetic a
// song is.
var tagEnergy = map[string]float64{
	"ambient": 0.1, "drone": 0.1, "classical": 0.2, "slowcore": 0.15, "chillout": 0.2,
	"chill": 0.25, "downtempo": 0.25, "ballad": 0.2, "sad": 0.2, "mellow": 0.2,
	"acoustic": 0.3, "singer-songwriter": 0.3, "lo-fi": 0.3, "lofi": 0.3, "indie folk": 0.3,
	"folk": 0.35, "trip-hop": 0.35, "trip hop": 0.35, "dream pop": 0.35, "jazz": 0.4,
	"soft rock": 0.4, "soul": 0.45, "blues": 0.45, "country": 0.45, "reggae": 0.45,
	"shoegaze": 0.5, "r&b": 0.5, "rnb": 0.5, "indie": 0.55, "psychedelic": 0.55,
	"indie pop": 0.55, "pop": 0.6, "indie rock": 0.6, "alternative": 0.6, "alternative rock": 0.6,
	"rock": 0.65, "classic rock": 0.65, "electronic": 0.65, "hip-hop": 0.65, "hip hop": 0.65,
	"new wave": 0.65, "post-punk": 0.65, "synthpop": 0.65, "emo": 0.65, "rap": 0.7,
	"funk": 0.7, "grunge": 0.7, "disco": 0.75, "garage rock": 0.75, "hard rock": 0.8,
	"pop punk": 0.8, "upbeat": 0.8, "house": 0.8, "dance": 0.85, "punk": 0.85,
	"punk rock": 0.85, "techno": 0.85, "trance": 0.85, "dubstep": 0.85, "party": 0.85,
	"energetic": 0.85, "edm": 0.9, "drum and bass": 0.9, "hardcore": 0.9, "metal": 0.9,
	"heavy metal": 0.9, "thrash metal": 0.95,
}

// energyOf guesses how energetic a song is, from 0 to 1, or −1 if neither
// its tempo nor its tags say.
func energyOf(bpm float64, tags []musicgraph.Tag) float64 {
	var sum, total float64
	for _, t := range tags {
		if e, ok := tagEnergy[strings.ToLower(strings.TrimSpace(t.Name))]; ok {
			w := max(t.Weight, 0.05)
			sum += e * w
			total += w
		}
	}
	switch {
	case total > 0 && bpm > 0:
		return 0.6*sum/total + 0.4*tempoEnergy(bpm)
	case total > 0:
		return sum / total
	case bpm > 0:
		return tempoEnergy(bpm)
	}
	return -1
}

// tempoEnergy is a tempo's energy: 70 BPM and under is calm, 170 and over
// is all out.
func tempoEnergy(bpm float64) float64 { return min(max((bpm-70)/100, 0), 1) }

// decadeOf reads a decade from tags like "80s" or "1980s", as the middle
// year of the decade most tagged, or 0.
func decadeOf(tags []musicgraph.Tag) int {
	best, most := 0, 0.0
	for _, t := range tags {
		n := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(t.Name)), "s")
		if len(n) != 2 && len(n) != 4 {
			continue
		}
		y, err := strconv.Atoi(n)
		if err != nil || y%10 != 0 {
			continue
		}
		switch {
		case len(n) == 2 && y >= 50:
			y += 1900
		case len(n) == 2:
			y += 2000
		case y < 1900 || y > 2100:
			continue
		}
		if t.Weight > most || best == 0 {
			best, most = y+5, t.Weight
		}
	}
	return best
}

// fit is how well b follows a in a set, and how close b comes to target
// (the energy curve's, or negative for none): adj is what it adds to b's
// score, and fit the mean of the fits known, from 0 to 1, or −1 if none
// is.
func fit(a, b sound, target float64) (adj, mean float64) {
	var sum float64
	n := 0
	add := func(f, w float64) {
		adj += w * (f - 0.5)
		sum += f
		n++
	}
	if a.bpm > 0 && b.bpm > 0 {
		d := math.Inf(1)
		for _, m := range []float64{1, 2, 0.5} {
			d = min(d, math.Abs(b.bpm*m/a.bpm-1))
		}
		add(ease(d, bpmEase, bpmSpan), bpmWeight)
	}
	if a.year > 0 && b.year > 0 {
		f := ease(math.Abs(float64(b.year-a.year)), eraEase, eraSpan)
		if a.year/10 == b.year/10 {
			f = 1
		}
		add(f, eraWeight)
	}
	if a.energy >= 0 && b.energy >= 0 {
		add(ease(math.Abs(b.energy-a.energy), energyEase, energySpan), energyWeight)
	}
	if target >= 0 && b.energy >= 0 {
		add(ease(math.Abs(b.energy-target), 0, curveSpan), curveWeight)
	}
	if n == 0 {
		return 0, -1
	}
	return adj, sum / float64(n)
}

// ease is 1 for d up to free, falling to 0 over span.
func ease(d, free, span float64) float64 {
	return 1 - min(max((d-free)/span, 0), 1)
}

// The energy curve: a room's energy rises gently over its first
// curveRise of a session, from curveStart to curvePeak, and settles to
// curveLate between curveSettle and curveSettled. The time of day moves
// it: lower in the small hours and the morning, a little higher in the
// evening.
const (
	curveStart, curvePeak, curveLate = 0.45, 0.75, 0.6
	curveRise                        = 90 * time.Minute
	curveSettle, curveSettled        = 3 * time.Hour, 5 * time.Hour
)

// dayShift is how the time of day moves the curve, by the hour: points
// (hour, shift) between which it's interpolated.
var dayShift = [][2]float64{{0, 0.05}, {2, 0}, {4, -0.15}, {7, -0.15}, {10, -0.05}, {13, 0}, {19, 0}, {22, 0.05}, {24, 0.05}}

// curve is the energy the room is at a moment, from 0 to 1, in a
// session that started at start. Times are read in the server's zone.
func curve(start, at time.Time) float64 {
	in := at.Sub(start)
	e := curveStart + (curvePeak-curveStart)*smooth(float64(in)/float64(curveRise))
	e -= (curvePeak - curveLate) * smooth(float64(in-curveSettle)/float64(curveSettled-curveSettle))
	local := at.Local()
	h := float64(local.Hour()) + float64(local.Minute())/60
	for i := 1; i < len(dayShift); i++ {
		if a, b := dayShift[i-1], dayShift[i]; h <= b[0] {
			e += a[1] + (b[1]-a[1])*(h-a[0])/(b[0]-a[0])
			break
		}
	}
	return min(max(e, 0.2), 0.9)
}

// smooth eases from 0 at x ≤ 0 to 1 at x ≥ 1.
func smooth(x float64) float64 {
	x = min(max(x, 0), 1)
	return x * x * (3 - 2*x)
}

// Flow is how a pick fits the set: what flow knew about it, and how well
// it follows the song before it.
type Flow struct {
	// BPM and Year are its tempo and when it came out, 0 if unknown;
	// Energy is how energetic it is and Target the energy curve's, from 0
	// to 1, −1 if unknown or the curve is off.
	BPM            float64
	Year           int
	Energy, Target float64
	// Fit is how well it follows the song before it and fits the curve,
	// from 0 to 1, or −1 if nothing was known to tell.
	Fit float64
	// Ahead are the songs the DJ planned after it, as "Artist – Title".
	// They're not queued: the DJ plans again after every change.
	Ahead []string
}

// flowing scores each of the shortlist's candidates on how well it follows
// what the room just heard, and on how well the best plan of planAhead
// songs it starts flows on; best first. tags are artists' tags by
// ArtistKey (Input.Tags). Songs whose tempo or year isn't cached are
// warmed for next time. curveOn follows the energy curve too.
func (e *Engine) flowing(ctx context.Context, p Profile, tags map[string][]musicgraph.Tag, pool []candidate, curveOn bool, now time.Time) []candidate {
	if len(pool) == 0 {
		return pool
	}
	last := unknownSound
	for _, t := range p.Before[:min(len(p.Before), flowReach)] {
		last = last.orElse(e.soundOf(ctx, t, tags[ArtistKey(artistOf(t))], nil))
	}
	target := func(i int) float64 {
		if !curveOn {
			return -1
		}
		return curve(p.SessionStart, now.Add(time.Duration(i)*songLength))
	}
	var cold []musicgraph.SongRef
	sounds := make([]sound, len(pool))
	for i, c := range pool {
		sounds[i] = e.soundOf(ctx, provider.Track{Title: c.song.Title, ISRC: c.song.ISRC, MBID: c.song.MBID, Artists: []provider.ArtistCredit{{Name: c.song.Artist.Name}}}, c.tags, &cold)
	}
	e.Graph.WarmTrack(cold...)

	out := slices.Clone(pool)
	for i := range out {
		c := &out[i]
		adj, f := fit(last, sounds[i], target(0))
		c.flow = Flow{BPM: sounds[i].bpm, Year: sounds[i].year, Energy: sounds[i].energy, Target: target(0), Fit: f}
		// The rest of the plan: from c, the best next song each time, by
		// score and flow, none by an artist already in it.
		used, artists := map[int]bool{i: true}, map[string]bool{c.artist: true}
		prev, ahead, steps := sounds[i], 0.0, 0
		for step := 1; step < planAhead; step++ {
			best, bestAdj, bestVal := -1, 0.0, math.Inf(-1)
			for j, o := range pool {
				if used[j] || artists[o.artist] {
					continue
				}
				a, _ := fit(prev, sounds[j], target(step))
				if v := o.score + a; v > bestVal {
					best, bestAdj, bestVal = j, a, v
				}
			}
			if best < 0 {
				break
			}
			used[best], artists[pool[best].artist] = true, true
			prev, ahead, steps = sounds[best], ahead+bestAdj, steps+1
			c.flow.Ahead = append(c.flow.Ahead, pool[best].song.Artist.Name+" – "+pool[best].song.Title)
		}
		if steps > 0 {
			ahead /= float64(steps)
		}
		c.score = math.Round((c.score+adj+aheadWeight*ahead)*1000) / 1000
	}
	slices.SortStableFunc(out, func(a, b candidate) int { return cmp.Compare(b.score, a.score) })
	return out
}

// soundOf reads what flow knows about a song: the graph's cache, then
// its service's tags. A song not cached is added to cold, if given.
func (e *Engine) soundOf(ctx context.Context, t provider.Track, tags []musicgraph.Tag, cold *[]musicgraph.SongRef) sound {
	ref := musicgraph.SongRef{Title: t.Title, Artist: musicgraph.ArtistRef{Name: artistOf(t)}, ISRC: t.ISRC, MBID: t.MBID}
	tr, ok, err := e.Graph.CachedTrack(ctx, ref)
	if !ok && err == nil && cold != nil {
		*cold = append(*cold, ref)
	}
	return soundOf(tr, t.BPM, t.Year, tags)
}
