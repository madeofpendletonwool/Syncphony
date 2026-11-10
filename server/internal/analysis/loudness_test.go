// SPDX-License-Identifier: AGPL-3.0-only

package analysis

import (
	"math"
	"testing"
)

// sine is seconds of a sine at hz and amplitude amp, as PCM.
func sine(hz, amp, seconds float64) []float32 {
	pcm := make([]float32, int(seconds*SampleRate))
	for i := range pcm {
		pcm[i] = float32(amp * math.Sin(2*math.Pi*hz*float64(i)/SampleRate))
	}
	return pcm
}

func lufsOf(t *testing.T, pcm []float32) float64 {
	t.Helper()
	m := analyze(t, f32le(pcm))
	if m.LUFS == nil {
		t.Fatal("no loudness")
	}
	return *m.LUFS
}

func TestLoudness(t *testing.T) {
	// BS.1770's reference: a full-scale 997 Hz sine reads -3.01 LUFS in
	// one channel, plus stereoLU for the mix. The K-weighting comes out a
	// little different at 22.05 kHz than at BS.1770's 48 kHz, a few tenths
	// of a LU, the same for every song.
	want := -3.01 + stereoLU
	if l := lufsOf(t, sine(997, 1, 10)); math.Abs(l-want) > 0.5 {
		t.Errorf("full-scale sine: %.2f LUFS, want %.2f", l, want)
	}
	// 20 dB quieter reads 20 LU less.
	if l := lufsOf(t, sine(997, 0.1, 10)); math.Abs(l-(want-20)) > 0.5 {
		t.Errorf("-20 dB sine: %.2f LUFS, want %.2f", l, want-20)
	}
	// Silence around the music doesn't make it quieter: it's gated out.
	gapped := append(append(make([]float32, 20*SampleRate), sine(997, 0.1, 10)...), make([]float32, 20*SampleRate)...)
	if l := lufsOf(t, gapped); math.Abs(l-(want-20)) > 0.5 {
		t.Errorf("with silence around it: %.2f LUFS, want %.2f", l, want-20)
	}
	// The ear's weighting: deep bass counts for less than the mids.
	if bass, mid := lufsOf(t, sine(30, 0.5, 10)), lufsOf(t, sine(997, 0.5, 10)); bass > mid-1 {
		t.Errorf("30 Hz %.2f LUFS, 1 kHz %.2f LUFS: bass should read quieter", bass, mid)
	}
	// Silence has none.
	if m := analyze(t, f32le(make([]float32, 5*SampleRate))); m.LUFS != nil {
		t.Errorf("silence: %v LUFS", *m.LUFS)
	}
}
