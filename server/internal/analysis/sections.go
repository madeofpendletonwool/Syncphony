// SPDX-License-Identifier: AGPL-3.0-only

package analysis

import "math"

const (
	// kernelBars is how many bars each side of a boundary are compared.
	kernelBars = 8
	// minSectionBars is the shortest section that's kept.
	minSectionBars = 4
	// envStep is how many analysis windows make one envelope frame (about 21.5 a second).
	envStep = 2
	// envRangeDB is how far below a band's loud level its envelope reaches 0.
	envRangeDB = 36.0
)

// sections splits the song where its sound changes: each bar is described
// by its spectrum and loudness, and a checkerboard kernel slid along the
// bars' self-similarity matrix peaks where the bars before a point are
// alike, the bars after it are alike, and the two differ (Foote).
func sections(beats []float64, first int, bandDB [][Bands]float64, rmsDB []float64) []Section {
	// Bars, by the window each starts at.
	var starts []float64
	for i := first; i < len(beats); i += 4 {
		starts = append(starts, beats[i])
	}
	if len(starts) < 2 {
		return nil
	}
	const dims = Bands + 1
	n := len(starts) - 1
	feats := make([][dims]float64, n)
	loud := make([]float64, n)
	for b := range n {
		from, to := int(starts[b]), min(int(starts[b+1]), len(rmsDB))
		if to <= from {
			to = min(from+1, len(rmsDB))
		}
		for t := from; t < to; t++ {
			for k := range Bands {
				feats[b][k] += bandDB[t][k]
			}
			feats[b][Bands] += rmsDB[t]
		}
		for k := range dims {
			feats[b][k] /= float64(to - from)
		}
		loud[b] = feats[b][Bands]
	}
	// Each dimension to zero mean and unit spread, so no band dominates.
	for k := range dims {
		var mean, sq float64
		for b := range n {
			mean += feats[b][k]
		}
		mean /= float64(n)
		for b := range n {
			sq += (feats[b][k] - mean) * (feats[b][k] - mean)
		}
		sd := math.Sqrt(sq / float64(n))
		for b := range n {
			feats[b][k] = (feats[b][k] - mean) / math.Max(sd, 1e-6)
		}
	}
	sim := func(i, j int) float64 {
		var d float64
		for k := range dims {
			x := feats[i][k] - feats[j][k]
			d += x * x
		}
		return math.Exp(-d / dims)
	}

	novelty := make([]float64, n)
	for c := 1; c < n; c++ {
		var s, w float64
		for i := -kernelBars; i < kernelBars; i++ {
			for j := -kernelBars; j < kernelBars; j++ {
				a, b := c+i, c+j
				if a < 0 || b < 0 || a >= n || b >= n {
					continue
				}
				// Gaussian taper; + within a side, − across the boundary.
				g := math.Exp(-0.5 * (float64(i*i+j*j) / (kernelBars * kernelBars / 4)))
				sign := 1.0
				if (i < 0) != (j < 0) {
					sign = -1
				}
				s += sign * g * sim(a, b)
				w += g
			}
		}
		novelty[c] = s / w
	}
	threshold := percentile(novelty, 0.5) + 0.5*spread(novelty)

	bounds := []int{0}
	for c := 1; c < n; c++ {
		if novelty[c] < threshold || c-bounds[len(bounds)-1] < minSectionBars || n-c < minSectionBars {
			continue
		}
		peak := true
		for d := -minSectionBars / 2; d <= minSectionBars/2; d++ {
			if j := c + d; j != c && j > 0 && j < n && novelty[j] > novelty[c] {
				peak = false
			}
		}
		if peak {
			bounds = append(bounds, c)
		}
	}

	lo, hi := percentile(loud, 0.05), percentile(loud, 0.95)
	out := make([]Section, len(bounds))
	for i, b := range bounds {
		end := n
		if i+1 < len(bounds) {
			end = bounds[i+1]
		}
		var l float64
		for j := b; j < end; j++ {
			l += loud[j]
		}
		l /= float64(end - b)
		e := 0.5
		if hi-lo > 0.5 {
			e = clamp01((l - lo) / (hi - lo))
		}
		start := frameMs(starts[b])
		if i == 0 {
			start = 0
		}
		out[i] = Section{StartMs: start, Energy: math.Round(e*100) / 100}
	}
	return out
}

func spread(xs []float64) float64 {
	var mean, sq float64
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))
	for _, x := range xs {
		sq += (x - mean) * (x - mean)
	}
	return math.Sqrt(sq / float64(len(xs)))
}

// envelopes packs the band levels and loudness into bytes, envStep windows
// to a frame (keeping the loudest, so hits aren't averaged away). Each band
// is scaled to its own loud level in the song, so the treble moves as
// visibly as the bass.
func envelopes(bandDB [][Bands]float64, rmsDB []float64) (bands, loudness []byte) {
	frames := (len(rmsDB) + envStep - 1) / envStep
	bands = make([]byte, frames*Bands)
	loudness = make([]byte, frames)
	var top [Bands]float64
	col := make([]float64, len(bandDB))
	for k := range Bands {
		for t := range bandDB {
			col[t] = bandDB[t][k]
		}
		top[k] = percentile(col, 0.98)
	}
	loudTop := percentile(rmsDB, 0.98)
	level := func(db, top float64) byte {
		return byte(math.Round(255 * clamp01((db-(top-envRangeDB))/envRangeDB)))
	}
	for f := range frames {
		for t := f * envStep; t < min((f+1)*envStep, len(rmsDB)); t++ {
			for k := range Bands {
				bands[f*Bands+k] = max(bands[f*Bands+k], level(bandDB[t][k], top[k]))
			}
			loudness[f] = max(loudness[f], level(rmsDB[t], loudTop))
		}
	}
	return bands, loudness
}

// features describe the song as a whole. loud is its 90th percentile level.
func features(rmsDB, centroid []float64, loud float64) Features {
	var c []float64
	for t, v := range centroid {
		if rmsDB[t] > loud-30 {
			c = append(c, v)
		}
	}
	bright := 0.0
	if len(c) > 0 {
		// 500 Hz is dull, 4 kHz bright.
		bright = clamp01(math.Log2(math.Max(percentile(c, 0.5), 1)/500) / 3)
	}
	quiet := percentile(rmsDB, 0.1)
	return Features{
		// RMS around −30 dBFS is a quiet recording, −8 a loud master.
		Energy:     round2(clamp01((percentile(rmsDB, 0.5) + 30) / 22)),
		Brightness: round2(bright),
		Dynamics:   round2(clamp01((loud - math.Max(quiet, loud-40)) / 30)),
	}
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }
