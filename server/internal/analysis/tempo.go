// SPDX-License-Identifier: AGPL-3.0-only

package analysis

import "math"

const (
	minBPM = 55.0
	maxBPM = 215.0
	// The tempo prior: songs cluster around priorBPM. Tuned so 84–160 BPM
	// drum patterns come out right; outside that, a song can read at half or
	// double its tempo (a 72 BPM ballad with busy hi-hats as 144), the
	// ambiguity every beat tracker has.
	priorBPM    = 128.0
	priorOctave = 0.6
	// tightness is how much the beat tracker resists changing tempo.
	tightness = 100.0
)

// onsetStrength is the flux with its slow trend taken out, scaled to unit
// spread: how sharply something starts in each window.
func onsetStrength(flux []float64) []float64 {
	const span = 16 // about 370 ms
	o := make([]float64, len(flux))
	var sum float64
	for i, v := range flux {
		sum += v
		if i >= span {
			sum -= flux[i-span]
		}
		mean := sum / float64(min(i+1, span))
		o[i] = math.Max(0, v-mean)
	}
	var sq float64
	for _, v := range o {
		sq += v * v
	}
	if sd := math.Sqrt(sq / float64(len(o))); sd > 0 {
		for i := range o {
			o[i] /= sd
		}
	}
	return o
}

// tempo is the beat period in windows (fractional), and how clear it is
// (0–1): the onset strength's correlation with itself a period later.
func tempo(flux []float64) (period, confidence float64) {
	// Blurred a little, so a period that falls between whole windows
	// (120 BPM is 21.53) scores as well as one that doesn't.
	o := smooth(onsetStrength(flux), 1.5)
	lo := int(math.Floor(frameRate * 60 / maxBPM))
	hi := int(math.Ceil(frameRate * 60 / minBPM))
	if len(o) < 2*hi {
		hi = len(o) / 2
	}
	if hi <= lo+2 {
		return frameRate * 60 / priorBPM, 0
	}
	var mean float64
	for _, v := range o {
		mean += v
	}
	mean /= float64(len(o))
	// ac picks the period; cov, centred, says how clear it is.
	ac := make([]float64, hi+2)
	cov := func(lag int) float64 {
		var s float64
		for t := 0; t+lag < len(o); t++ {
			s += (o[t] - mean) * (o[t+lag] - mean)
		}
		return s / float64(len(o)-lag)
	}
	for lag := lo - 1; lag <= hi+1 && lag < len(o); lag++ {
		var s float64
		for t := 0; t+lag < len(o); t++ {
			s += o[t] * o[t+lag]
		}
		ac[lag] = s / float64(len(o)-lag)
	}
	weight := func(lag float64) float64 {
		octaves := math.Log2(frameRate * 60 / lag / priorBPM)
		return math.Exp(-0.5 * (octaves / priorOctave) * (octaves / priorOctave))
	}
	best, bestScore := lo, math.Inf(-1)
	for lag := lo; lag <= hi; lag++ {
		s := ac[lag] * weight(float64(lag))
		if s > bestScore {
			best, bestScore = lag, s
		}
	}
	// A parabola through the peak finds it between whole windows.
	period = float64(best)
	if a, b, c := ac[best-1], ac[best], ac[best+1]; a-2*b+c < 0 {
		period += 0.5 * (a - c) / (a - 2*b + c)
	}
	if v := cov(0); v > 0 {
		confidence = clamp01(cov(best) / v)
	}
	return period, confidence
}

// trackBeats places beats on the onset strength o, keeping close to period
// (in windows): Ellis's dynamic programming beat tracker. Each window's
// score is its own onset plus the best score of a beat about a period
// before it, less a penalty for straying from the period, if that's
// worth having. Returns the
// beats' times in windows, refined to fractions of a window.
func trackBeats(o []float64, period float64) []float64 {
	n := len(o)
	if n == 0 || period <= 0 {
		return nil
	}
	// Smooth the onsets a little, so a beat may land just off a peak.
	local := smooth(o, period/32)
	score := make([]float64, n)
	back := make([]int, n)
	from, to := int(math.Round(period/2)), int(math.Round(2*period))
	for t := range n {
		back[t] = -1
		best := 0.0
		for p := t - to; p <= t-from; p++ {
			if p < 0 {
				continue
			}
			d := math.Log(float64(t-p) / period)
			if s := score[p] - tightness*d*d; back[t] < 0 || s > best {
				best, back[t] = s, p
			}
		}
		score[t] = local[t]
		if back[t] >= 0 && best > 0 {
			score[t] += best
		} else {
			// Nothing worth following before this (the silence before
			// the song): start afresh, so the silence can't set the phase.
			back[t] = -1
		}
	}
	// The last beat: the best score in the final stretch.
	end := n - 1
	for t := max(0, n-int(2*period)); t < n; t++ {
		if score[t] > score[end] {
			end = t
		}
	}
	var beats []float64
	for t := end; t >= 0; t = back[t] {
		beats = append(beats, refine(o, t))
	}
	for i, j := 0, len(beats)-1; i < j; i, j = i+1, j-1 {
		beats[i], beats[j] = beats[j], beats[i]
	}
	return beats
}

// refine moves a beat at window t to the onset peak's centre, between windows.
func refine(o []float64, t int) float64 {
	if t <= 0 || t >= len(o)-1 {
		return float64(t)
	}
	a, b, c := o[t-1], o[t], o[t+1]
	if b < a || b < c || a-2*b+c >= 0 {
		return float64(t)
	}
	return float64(t) + 0.5*(a-c)/(a-2*b+c)
}

// smooth convolves x with a Gaussian of the given width, in samples.
func smooth(x []float64, sigma float64) []float64 {
	if sigma < 0.5 {
		return x
	}
	r := int(math.Ceil(3 * sigma))
	k := make([]float64, 2*r+1)
	for i := range k {
		d := float64(i-r) / sigma
		k[i] = math.Exp(-0.5 * d * d)
	}
	out := make([]float64, len(x))
	for t := range x {
		var s, w float64
		for i, kv := range k {
			if j := t + i - r; j >= 0 && j < len(x) {
				s += x[j] * kv
				w += kv
			}
		}
		out[t] = s / w
	}
	return out
}

// downbeat picks which of the first four beats starts a bar: the one whose
// place in the bar gets the most kick drum, and the most energy.
func downbeat(beats []float64, lowFlux, rmsDB []float64) int {
	at := func(xs []float64, t float64) float64 {
		i := int(math.Round(t))
		v := math.Inf(-1)
		for j := max(0, i-1); j <= min(len(xs)-1, i+1); j++ {
			v = math.Max(v, xs[j])
		}
		return v
	}
	var kick, loud [4]float64
	var count [4]int
	for i, t := range beats {
		kick[i%4] += at(lowFlux, t)
		loud[i%4] += at(rmsDB, t)
		count[i%4]++
	}
	var kickSum float64
	for p := range 4 {
		kick[p] /= float64(max(count[p], 1))
		loud[p] /= float64(max(count[p], 1))
		kickSum += kick[p]
	}
	best, bestScore := 0, math.Inf(-1)
	for p := range 4 {
		s := loud[p] / 6 // a dB or so counts for about as much as…
		if kickSum > 0 {
			s += 4 * kick[p] / kickSum // …a share of the kicks.
		}
		if s > bestScore {
			best, bestScore = p, s
		}
	}
	return best
}
