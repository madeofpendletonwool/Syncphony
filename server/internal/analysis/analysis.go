// SPDX-License-Identifier: AGPL-3.0-only

// Package analysis works out a song's beat map (MAD-773): its tempo, where
// the beats and bars fall, how loud each part of the spectrum is from
// moment to moment, and where its sections change. Every screen in a room
// uses the map to move with the music in step, without hearing it.
//
// The song is decoded once, to mono at 22.05 kHz, and analysed in one pass
// of short overlapping windows (46 ms, every 23 ms). Onsets come from
// spectral flux; tempo from the onset curve's autocorrelation with a
// prior around 120 BPM; beats from dynamic programming (Ellis, 2007);
// downbeats from where the kicks land; sections from the novelty of a
// bar-by-bar self-similarity matrix (Foote, 2000).
package analysis

import (
	"errors"
	"io"
	"math"
	"slices"
)

const (
	// SampleRate is the rate songs are decoded to for analysis.
	SampleRate = 22050
	// Version changes whenever an older map should be worked out again.
	Version = 1
	// Bands is how many spectrum bands the map's envelope has.
	Bands = 12

	window = 1024
	hop    = 512
	minHz  = 40.0
	// lowHz bounds the kick drum's part of the spectrum, for downbeats.
	lowHz = 200.0
	// Below these a song has no beat: its spectrum barely changes (music
	// is in the tens, a steady tone under one), or its changes don't repeat.
	minFlux       = 2.0
	minConfidence = 0.1
	// noiseFloor is about −68 dB below a full-scale sine's peak bin
	// (window/4): under anything audible in a mix, over 8-bit noise.
	noiseFloor = 0.1
)

// frameRate is how many analysis windows there are a second.
const frameRate = float64(SampleRate) / hop

// Map is a song's beat map.
type Map struct {
	Version    int   `json:"v"`
	DurationMs int64 `json:"durationMs"`
	// BPM is the tempo, 0 if the song has no steady beat (or no sound).
	BPM float64 `json:"bpm"`
	// Confidence is how clear the beat is, 0–1.
	Confidence float64 `json:"confidence"`
	// BeatsMs are the beats, in ms from the start.
	BeatsMs []int64 `json:"beats"`
	// Downbeat is the index in BeatsMs of the first bar's first beat;
	// every fourth beat after it starts a bar.
	Downbeat int       `json:"downbeat"`
	Sections []Section `json:"sections"`
	// FrameRate is how many envelope frames there are a second.
	FrameRate float64 `json:"frameRate"`
	// BandsEnv is each frame's level in each band, low to high, 0–255:
	// Bands bytes a frame. Each band is scaled to its own range in the song.
	BandsEnv []byte `json:"bands"`
	// Loudness is each frame's overall level, 0–255, scaled to the song.
	Loudness []byte   `json:"loudness"`
	Features Features `json:"features"`
}

// Section is a part of the song: an intro, a verse, a drop.
type Section struct {
	StartMs int64 `json:"startMs"`
	// Energy is how loud it is next to the rest of the song, 0–1.
	Energy float64 `json:"energy"`
}

// Features describe the whole song, for choosing how it looks.
type Features struct {
	// Energy is how loud it's mastered, 0 (quiet acoustic) – 1 (a club track).
	Energy float64 `json:"energy"`
	// Brightness is how much treble there is, 0–1.
	Brightness float64 `json:"brightness"`
	// Dynamics is how much its loudness changes, 0–1.
	Dynamics float64 `json:"dynamics"`
}

// Analyze reads a song as mono 32-bit float samples at SampleRate and
// works out its beat map.
func Analyze(pcm io.Reader) (Map, error) {
	a := newAnalyzer()
	buf := make([]byte, 64<<10)
	var carry int
	for {
		n, err := pcm.Read(buf[carry:])
		n += carry
		whole := n - n%4
		a.write(buf[:whole])
		carry = copy(buf, buf[whole:n])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Map{}, err
		}
	}
	return a.finish(), nil
}

// analyzer takes samples as they're decoded and keeps a few numbers per window.
type analyzer struct {
	fft     *fft
	hann    []float64
	pending []float64
	samples int64

	spec    []complex128
	prevLog []float64
	bandOf  []int
	lowBins int

	bandDB   [][Bands]float64
	flux     []float64
	lowFlux  []float64
	rmsDB    []float64
	centroid []float64
}

func newAnalyzer() *analyzer {
	a := &analyzer{
		fft:     newFFT(window),
		hann:    make([]float64, window),
		spec:    make([]complex128, window),
		prevLog: make([]float64, window/2+1),
		bandOf:  make([]int, window/2+1),
		// Half a window of silence first, so window t is centred on t·hop.
		pending: make([]float64, window/2, 8*window),
	}
	for i := range a.hann {
		a.hann[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(window))
	}
	nyquist := float64(SampleRate) / 2
	for k := range a.bandOf {
		hz := binHz(k)
		a.bandOf[k] = -1
		if hz >= minHz {
			b := int(float64(Bands) * math.Log(hz/minHz) / math.Log(nyquist/minHz))
			a.bandOf[k] = min(b, Bands-1)
		}
		if hz < lowHz {
			a.lowBins = k + 1
		}
	}
	return a
}

func binHz(k int) float64 { return float64(k) * SampleRate / window }

func (a *analyzer) write(b []byte) {
	for i := 0; i+4 <= len(b); i += 4 {
		bits := uint32(b[i]) | uint32(b[i+1])<<8 | uint32(b[i+2])<<16 | uint32(b[i+3])<<24
		a.pending = append(a.pending, float64(math.Float32frombits(bits)))
	}
	a.samples += int64(len(b) / 4)
	start := 0
	for len(a.pending)-start >= window {
		a.frame(a.pending[start : start+window])
		start += hop
	}
	a.pending = append(a.pending[:0], a.pending[start:]...)
}

func (a *analyzer) frame(x []float64) {
	var sq float64
	for i, v := range x {
		sq += v * v
		a.spec[i] = complex(v*a.hann[i], 0)
	}
	a.fft.transform(a.spec)

	var bands [Bands]float64
	var counts [Bands]int
	var flux, low, power, weighted float64
	for k := range a.prevLog {
		re, im := real(a.spec[k]), imag(a.spec[k])
		p := re*re + im*im
		m := math.Sqrt(p)
		// Log-compressed magnitude, so flux hears quiet notes too, but
		// nothing under the noise floor: hiss and quantisation noise
		// change every window, and would look like a beat.
		lm := math.Log1p(10 * math.Max(0, m-noiseFloor))
		if k > 0 {
			if d := lm - a.prevLog[k]; d > 0 {
				flux += d
				if k < a.lowBins {
					low += d
				}
			}
		}
		a.prevLog[k] = lm
		if b := a.bandOf[k]; b >= 0 {
			bands[b] += p
			counts[b]++
		}
		power += p
		weighted += p * binHz(k)
	}
	var db [Bands]float64
	for b := range bands {
		db[b] = 10 * math.Log10(bands[b]/float64(max(counts[b], 1))+1e-12)
	}
	a.bandDB = append(a.bandDB, db)
	a.flux = append(a.flux, flux)
	a.lowFlux = append(a.lowFlux, low)
	a.rmsDB = append(a.rmsDB, 20*math.Log10(math.Sqrt(sq/window)+1e-9))
	c := 0.0
	if power > 1e-9 {
		c = weighted / power
	}
	a.centroid = append(a.centroid, c)
}

func (a *analyzer) finish() Map {
	m := Map{
		Version:    Version,
		DurationMs: a.samples * 1000 / SampleRate,
		BeatsMs:    []int64{},
		Sections:   []Section{},
		FrameRate:  frameRate / envStep,
	}
	n := len(a.flux)
	if n == 0 {
		return m
	}
	loud := percentile(a.rmsDB, 0.9)
	if loud < -60 {
		// Silence: nothing to follow.
		return m
	}

	period, conf := tempo(a.flux)
	var beats []float64
	if steady(a.flux, conf) {
		beats = trimQuiet(trackBeats(onsetStrength(a.flux), period), a.rmsDB, loud-30)
	}
	if len(beats) >= 8 {
		m.BeatsMs = make([]int64, len(beats))
		for i, t := range beats {
			m.BeatsMs[i] = frameMs(t)
		}
		m.BPM = bpmOf(m.BeatsMs)
		m.Confidence = conf
		m.Downbeat = downbeat(beats, a.lowFlux, a.rmsDB)
		m.Sections = sections(beats, m.Downbeat, a.bandDB, a.rmsDB)
	}
	if len(m.Sections) == 0 {
		m.Sections = []Section{{StartMs: 0, Energy: 0.5}}
	}

	m.BandsEnv, m.Loudness = envelopes(a.bandDB, a.rmsDB)
	m.Features = features(a.rmsDB, a.centroid, loud)
	return m
}

// steady reports whether the song has a beat to follow: something starts
// often enough (a held tone or a drone barely changes), and the starts
// repeat.
func steady(flux []float64, confidence float64) bool {
	var sum float64
	for _, f := range flux {
		sum += f
	}
	return sum/float64(len(flux)) >= minFlux && confidence >= minConfidence
}

// frameMs is the time of window t, which is centred on t·hop.
func frameMs(t float64) int64 { return int64(math.Round(t * hop * 1000 / SampleRate)) }

// bpmOf is the tempo the beats keep: the slope of a line through them,
// which a few stray beats barely move, and which isn't limited to the
// analysis windows' resolution as single gaps are.
func bpmOf(beats []int64) float64 {
	n := float64(len(beats))
	mi := (n - 1) / 2
	var mt float64
	for _, b := range beats {
		mt += float64(b)
	}
	mt /= n
	var num, den float64
	for i, b := range beats {
		num += (float64(i) - mi) * (float64(b) - mt)
		den += (float64(i) - mi) * (float64(i) - mi)
	}
	if den == 0 || num <= 0 {
		return 0
	}
	return math.Round(60000/(num/den)*10) / 10
}

// trimQuiet drops beats in the silence before a song starts and after it ends.
func trimQuiet(beats []float64, rmsDB []float64, floor float64) []float64 {
	quiet := func(t float64) bool {
		i := min(int(math.Round(t)), len(rmsDB)-1)
		return rmsDB[i] < floor
	}
	for len(beats) > 0 && quiet(beats[0]) {
		beats = beats[1:]
	}
	for len(beats) > 0 && quiet(beats[len(beats)-1]) {
		beats = beats[:len(beats)-1]
	}
	return beats
}

// percentile is the value p (0–1) of the way up xs, sorted. xs is unchanged.
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[min(int(p*float64(len(s)-1)+0.5), len(s)-1)]
}

func clamp01(x float64) float64 { return math.Max(0, math.Min(1, x)) }
