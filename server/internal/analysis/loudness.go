// SPDX-License-Identifier: AGPL-3.0-only

package analysis

import "math"

// Integrated loudness, after ITU-R BS.1770-4 (EBU R128): the signal is
// K-weighted (a high shelf for the head, a high pass for the ear), its
// power taken over 400 ms blocks every 100 ms, and blocks under -70 LUFS,
// then under 10 LU below the rest, left out, so silence and quiet intros
// don't count. Rooms use it to bring songs to a similar volume.
//
// It's measured on the mono mix the analysis decodes (ffmpeg mixes each
// channel in at -3 dB), which a stereo meter reads a little louder: by
// 0.8 LU on average over a sample of real songs against ffmpeg's ebur128,
// within ±0.7 LU from a near-mono 60s single to a wide modern mix. LUFS
// adds that back.

const (
	stepSamples  = SampleRate / 10 // 100 ms
	absoluteGate = -70.0
	relativeGate = -10.0
	// stereoLU is what a stereo meter reads over the mono mix, typically.
	stereoLU = 0.8
)

// biquad is a second-order IIR filter, direct form I.
type biquad struct {
	b0, b1, b2, a1, a2 float64
	x1, x2, y1, y2     float64
}

func (f *biquad) next(x float64) float64 {
	y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
	f.x2, f.x1 = f.x1, x
	f.y2, f.y1 = f.y1, y
	return y
}

// kWeighting is BS.1770's filter pair, designed for fs.
func kWeighting(fs float64) [2]biquad {
	// The high shelf: about +4 dB above 1.7 kHz.
	g, q, fc := 3.999843853973347, 0.7071752369554196, 1681.974450955533
	a := math.Pow(10, g/40)
	w := 2 * math.Pi * fc / fs
	alpha := math.Sin(w) / (2 * q)
	c := math.Cos(w)
	sa := 2 * math.Sqrt(a) * alpha
	a0 := (a + 1) - (a-1)*c + sa
	shelf := biquad{
		b0: a * ((a + 1) + (a-1)*c + sa) / a0,
		b1: -2 * a * ((a - 1) + (a+1)*c) / a0,
		b2: a * ((a + 1) + (a-1)*c - sa) / a0,
		a1: 2 * ((a - 1) - (a+1)*c) / a0,
		a2: ((a + 1) - (a-1)*c - sa) / a0,
	}
	// The high pass: rolls off under about 38 Hz.
	q, fc = 0.5003270373238773, 38.13547087602444
	w = 2 * math.Pi * fc / fs
	alpha = math.Sin(w) / (2 * q)
	c = math.Cos(w)
	a0 = 1 + alpha
	hp := biquad{
		b0: (1 + c) / 2 / a0,
		b1: -(1 + c) / a0,
		b2: (1 + c) / 2 / a0,
		a1: -2 * c / a0,
		a2: (1 - alpha) / a0,
	}
	return [2]biquad{shelf, hp}
}

// meter takes samples and keeps the K-weighted power of each 100 ms.
type meter struct {
	k     [2]biquad
	sum   float64
	n     int
	steps []float64
}

func newMeter() *meter { return &meter{k: kWeighting(SampleRate)} }

func (m *meter) add(x float64) {
	y := m.k[1].next(m.k[0].next(x))
	m.sum += y * y
	if m.n++; m.n == stepSamples {
		m.steps = append(m.steps, m.sum/stepSamples)
		m.sum, m.n = 0, 0
	}
}

// lufs is the integrated loudness, as a stereo meter would read it. ok is
// false if nothing in the song is over the absolute gate.
func (m *meter) lufs() (lufs float64, ok bool) {
	var blocks []float64
	for i := 0; i+4 <= len(m.steps); i++ {
		blocks = append(blocks, (m.steps[i]+m.steps[i+1]+m.steps[i+2]+m.steps[i+3])/4)
	}
	level := func(power float64) float64 { return -0.691 + 10*math.Log10(power) }
	mean := func(over float64) (float64, bool) {
		var s float64
		var n int
		for _, b := range blocks {
			if b > 0 && level(b) > over {
				s += b
				n++
			}
		}
		if n == 0 {
			return 0, false
		}
		return s / float64(n), true
	}
	ungated, ok := mean(absoluteGate)
	if !ok {
		return 0, false
	}
	gated, ok := mean(math.Max(absoluteGate, level(ungated)+relativeGate))
	if !ok {
		return 0, false
	}
	return level(gated) + stereoLU, true
}
