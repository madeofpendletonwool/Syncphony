// SPDX-License-Identifier: AGPL-3.0-only

package analysis

import (
	"math"
	"math/cmplx"
)

// fft is an in-place radix-2 FFT for one window size, with its twiddles
// and bit-reversal table worked out once.
type fft struct {
	n       int
	rev     []int
	twiddle []complex128
}

func newFFT(n int) *fft {
	if n&(n-1) != 0 {
		panic("analysis: FFT size must be a power of two")
	}
	bits := 0
	for 1<<bits < n {
		bits++
	}
	f := &fft{n: n, rev: make([]int, n), twiddle: make([]complex128, n/2)}
	for i := range n {
		r := 0
		for b := range bits {
			if i&(1<<b) != 0 {
				r |= 1 << (bits - 1 - b)
			}
		}
		f.rev[i] = r
	}
	for i := range n / 2 {
		f.twiddle[i] = cmplx.Exp(complex(0, -2*math.Pi*float64(i)/float64(n)))
	}
	return f
}

// transform replaces x with its discrete Fourier transform.
func (f *fft) transform(x []complex128) {
	for i, r := range f.rev {
		if i < r {
			x[i], x[r] = x[r], x[i]
		}
	}
	for size := 2; size <= f.n; size <<= 1 {
		half, step := size/2, f.n/size
		for start := 0; start < f.n; start += size {
			for k := range half {
				t := f.twiddle[k*step] * x[start+k+half]
				x[start+k+half] = x[start+k] - t
				x[start+k] += t
			}
		}
	}
}
