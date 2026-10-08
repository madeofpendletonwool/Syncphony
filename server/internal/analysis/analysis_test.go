// SPDX-License-Identifier: AGPL-3.0-only

package analysis

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"math/rand/v2"
	"testing"
)

// song synthesises a drum pattern: a kick on 1 (heavier) and 3, a snare on 2 and 4,
// hi-hats on the eighths, starting lead seconds in. From bar loudFrom on,
// a bass line and louder drums come in (a "drop"). Returns the PCM and the
// true beat times in ms.
func song(bpm float64, bars, loudFrom int, lead float64) ([]byte, []float64) {
	period := 60 / bpm
	total := lead + float64(bars*4)*period + 1
	pcm := make([]float32, int(total*SampleRate))
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // repeatable noise for a test signal
	add := func(at float64, dur float64, f func(t float64) float64) {
		i0 := int(at * SampleRate)
		for i := 0; i < int(dur*SampleRate) && i0+i < len(pcm); i++ {
			pcm[i0+i] += float32(f(float64(i) / SampleRate))
		}
	}
	kick := func(gain float64) func(float64) float64 {
		return func(t float64) float64 {
			f := 50 + 100*math.Exp(-t*30)
			return gain * math.Sin(2*math.Pi*f*t) * math.Exp(-t*12)
		}
	}
	noise := func(gain, decay float64) func(float64) float64 {
		return func(t float64) float64 { return gain * (rng.Float64()*2 - 1) * math.Exp(-t*decay) }
	}
	var beats []float64
	for b := range bars * 4 {
		at := lead + float64(b)*period
		beats = append(beats, at*1000)
		loud := b/4 >= loudFrom
		g := 0.35
		if loud {
			g = 0.6
		}
		switch b % 4 {
		case 0:
			// Like most songs, the one is a little heavier than the three.
			add(at, 0.25, kick(g*1.6))
		case 2:
			add(at, 0.25, kick(g*1.3))
		case 1, 3:
			add(at, 0.15, noise(g*0.6, 25))
		}
		add(at, 0.05, noise(g*0.15, 80))
		add(at+period/2, 0.05, noise(g*0.12, 80))
		if loud {
			add(at, period*0.9, func(t float64) float64 { return 0.25 * math.Sin(2*math.Pi*55*t) })
		}
	}
	return f32le(pcm), beats
}

func f32le(pcm []float32) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, pcm)
	return buf.Bytes()
}

func analyze(t *testing.T, pcm []byte) Map {
	t.Helper()
	m, err := Analyze(bytes.NewReader(pcm))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTempoAndBeats(t *testing.T) {
	for _, bpm := range []float64{84, 96, 110, 120, 128, 140, 150, 160} {
		pcm, truth := song(bpm, 32, 99, 0.37)
		m := analyze(t, pcm)
		if math.Abs(m.BPM-bpm) > 1 {
			t.Errorf("%v BPM: found %v", bpm, m.BPM)
			continue
		}
		// Nearly every beat within 25 ms of a true one.
		hits := 0
		for _, b := range m.BeatsMs {
			for _, tr := range truth {
				if math.Abs(float64(b)-tr) <= 25 {
					hits++
					break
				}
			}
		}
		if len(m.BeatsMs) < len(truth)*9/10 || hits < len(m.BeatsMs)*95/100 {
			t.Errorf("%v BPM: %d beats found, %d of them on time, of %d", bpm, len(m.BeatsMs), hits, len(truth))
		}
		// The first downbeat lands on a true "one".
		if len(m.BeatsMs) > m.Downbeat {
			d := float64(m.BeatsMs[m.Downbeat])
			n := int(math.Round((d - truth[0]) / (60000 / bpm)))
			if n%4 != 0 {
				t.Errorf("%v BPM: downbeat on beat %d of the bar", bpm, n%4+1)
			}
		}
	}
}

func TestBeatOffset(t *testing.T) {
	// Beats aren't consistently early or late.
	pcm, truth := song(120, 32, 99, 0.5)
	m := analyze(t, pcm)
	var sum float64
	var n int
	for _, b := range m.BeatsMs {
		for _, tr := range truth {
			if d := float64(b) - tr; math.Abs(d) <= 40 {
				sum += d
				n++
				break
			}
		}
	}
	if n == 0 || math.Abs(sum/float64(n)) > 8 {
		t.Errorf("mean offset %.1f ms over %d beats", sum/float64(max(n, 1)), n)
	}
}

func TestSections(t *testing.T) {
	const bpm = 124.0
	pcm, truth := song(bpm, 48, 24, 0.2)
	m := analyze(t, pcm)
	if len(m.Sections) < 2 {
		t.Fatalf("sections: %+v", m.Sections)
	}
	drop := truth[24*4]
	found := false
	for i, s := range m.Sections[1:] {
		if math.Abs(float64(s.StartMs)-drop) < 60000/bpm*4+50 {
			found = true
			if s.Energy <= m.Sections[i].Energy {
				t.Errorf("the drop is no louder: %+v", m.Sections)
			}
		}
	}
	if !found {
		t.Errorf("no section starts at the drop (%.0f ms): %+v", drop, m.Sections)
	}
	if m.Sections[0].StartMs != 0 {
		t.Errorf("first section starts at %d", m.Sections[0].StartMs)
	}
}

func TestEnvelopes(t *testing.T) {
	pcm, _ := song(120, 16, 8, 0)
	m := analyze(t, pcm)
	frames := len(m.Loudness)
	want := float64(m.DurationMs) / 1000 * m.FrameRate
	if math.Abs(float64(frames)-want) > 3 {
		t.Errorf("%d frames for %d ms at %.2f/s", frames, m.DurationMs, m.FrameRate)
	}
	if len(m.BandsEnv) != frames*Bands {
		t.Errorf("%d band bytes for %d frames", len(m.BandsEnv), frames)
	}
	// The bass comes in with the drop: the lowest bands get louder.
	mean := func(band, from, to int) float64 {
		var s float64
		for f := from; f < to; f++ {
			s += float64(m.BandsEnv[f*Bands+band])
		}
		return s / float64(to-from)
	}
	half := frames / 2
	if before, after := mean(1, 0, half-20), mean(1, half+20, frames-20); after <= before+20 {
		t.Errorf("band 1 before the bass %.0f, after %.0f", before, after)
	}
	if m.Features.Energy <= 0 || m.Features.Energy > 1 || m.Features.Brightness < 0 || m.Features.Brightness > 1 {
		t.Errorf("features out of range: %+v", m.Features)
	}
}

func TestNoBeat(t *testing.T) {
	// A steady tone, like the dev library's songs, has no beat to follow.
	pcm := make([]float32, 20*SampleRate)
	for i := range pcm {
		pcm[i] = float32(0.2 * math.Sin(2*math.Pi*440*float64(i)/SampleRate))
	}
	m := analyze(t, f32le(pcm))
	if m.BPM != 0 || len(m.BeatsMs) != 0 {
		t.Errorf("a steady tone: %v BPM, %d beats", m.BPM, len(m.BeatsMs))
	}
	if len(m.Loudness) == 0 || len(m.Sections) != 1 {
		t.Errorf("a steady tone still has envelopes and one section: %d frames, %+v", len(m.Loudness), m.Sections)
	}
}

func TestSilence(t *testing.T) {
	m := analyze(t, make([]byte, 4*5*SampleRate))
	if m.BPM != 0 || len(m.BeatsMs) != 0 {
		t.Errorf("silence: %+v", m)
	}
	if m.DurationMs != 5000 {
		t.Errorf("duration %d", m.DurationMs)
	}
}

func TestOddReads(t *testing.T) {
	// Reads that split a sample in two still decode.
	pcm, _ := song(120, 8, 99, 0)
	whole := analyze(t, pcm)
	m, err := Analyze(&trickle{b: pcm, n: 7})
	if err != nil {
		t.Fatal(err)
	}
	if m.BPM != whole.BPM || len(m.BeatsMs) != len(whole.BeatsMs) {
		t.Errorf("trickled %v BPM / %d beats, whole %v / %d", m.BPM, len(m.BeatsMs), whole.BPM, len(whole.BeatsMs))
	}
}

type trickle struct {
	b []byte
	n int
}

func (r *trickle) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p[:min(len(p), r.n)], r.b)
	r.b = r.b[n:]
	return n, nil
}
