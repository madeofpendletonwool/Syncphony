// SPDX-License-Identifier: AGPL-3.0-only

// Package palette extracts a color palette from album art, so every phone
// in a room tints itself the same way without doing the work itself.
package palette

import (
	"image"
	"math"

	"golang.org/x/image/draw"
)

// Palette is the colors of a piece of artwork. Every role is always set:
// when the art has nothing that fits one, it's derived from the others.
type Palette struct {
	// Accent is the dominant vivid hue, which tints the whole app, or nil
	// for grayscale art. Its chroma is clamped to read well in both themes.
	Accent *Accent `json:"accent"`
	// Dominant is the most common color.
	Dominant Color `json:"dominant"`
	// Vibrant is the most colorful one that's neither near black nor white.
	Vibrant Color `json:"vibrant"`
	// Muted is a common, quieter color.
	Muted Color `json:"muted"`
	// Dark and Light are the art's darkest and lightest common colors.
	Dark  Color `json:"dark"`
	Light Color `json:"light"`
}

// Accent is a hue and chroma.
type Accent struct {
	H float64 `json:"h"`
	C float64 `json:"c"`
}

// sample is the size art is shrunk to first: plenty for a palette.
const sample = 48

// Extract computes img's palette.
func Extract(img image.Image) Palette {
	small := image.NewRGBA(image.Rect(0, 0, sample, sample))
	draw.ApproxBiLinear.Scale(small, small.Bounds(), img, img.Bounds(), draw.Src, nil)
	px := make([]lab, 0, sample*sample)
	for i := 0; i+3 < len(small.Pix); i += 4 {
		if small.Pix[i+3] < 128 {
			continue
		}
		px = append(px, oklab(small.Pix[i], small.Pix[i+1], small.Pix[i+2]))
	}
	return fromPixels(px)
}

func fromPixels(px []lab) Palette {
	p := Palette{Accent: pickAccent(px)}
	cs := cluster(px)
	if len(cs) == 0 {
		// Fully transparent: a neutral palette.
		cs = []clusterStat{{n: 1, color: Color{L: 0.5}}}
	}
	total := 0
	for _, c := range cs {
		total += c.n
	}
	best := func(score func(c clusterStat, share float64) float64) (Color, bool) {
		var top Color
		topScore := 0.0
		for _, c := range cs {
			if s := score(c, float64(c.n)/float64(total)); s > topScore {
				top, topScore = c.color, s
			}
		}
		return top, topScore > 0
	}

	p.Dominant, _ = best(func(_ clusterStat, share float64) float64 { return share })
	// The hue and chroma the derived roles take after.
	base := Color{H: p.Dominant.H, C: p.Dominant.C}
	if p.Accent != nil {
		base = Color{H: p.Accent.H, C: p.Accent.C}
	}

	var ok bool
	if p.Vibrant, ok = best(func(c clusterStat, share float64) float64 {
		if c.color.C < 0.08 || share < 0.01 {
			return 0
		}
		return c.color.C * math.Max(0, 1-math.Abs(c.color.L-0.65)/0.4) * math.Sqrt(share)
	}); !ok {
		p.Vibrant = Color{L: 0.65, C: base.C, H: base.H}
	}
	if p.Muted, ok = best(func(c clusterStat, share float64) float64 {
		if c.color.C < 0.02 || c.color.C >= 0.1 || c.color.L < 0.3 || c.color.L > 0.75 {
			return 0
		}
		return share
	}); !ok {
		p.Muted = Color{L: 0.55, C: math.Min(base.C, 0.06), H: base.H}
	}
	if p.Dark, ok = best(func(c clusterStat, share float64) float64 {
		if c.color.L >= 0.35 || share < 0.02 {
			return 0
		}
		return share
	}); !ok {
		p.Dark = Color{L: 0.22, C: math.Min(base.C, 0.05), H: base.H}
	}
	if p.Light, ok = best(func(c clusterStat, share float64) float64 {
		if c.color.L <= 0.8 || share < 0.02 {
			return 0
		}
		return share
	}); !ok {
		p.Light = Color{L: 0.92, C: math.Min(base.C, 0.04), H: base.H}
	}
	for _, c := range []*Color{&p.Dominant, &p.Vibrant, &p.Muted, &p.Dark, &p.Light} {
		*c = c.rounded()
	}
	if p.Accent != nil {
		p.Accent.H = math.Round(p.Accent.H*100) / 100
		p.Accent.C = math.Round(p.Accent.C*10000) / 10000
	}
	return p
}

type clusterStat struct {
	n     int
	color Color
}

// cluster groups pixels into boxes of lightness, hue and chroma, and
// averages each box in OKLab.
func cluster(px []lab) []clusterStat {
	type key struct{ l, h, c int }
	type sum struct {
		n       int
		l, a, b float64
	}
	sums := map[key]*sum{}
	var order []key
	for _, p := range px {
		lch := p.lch()
		k := key{l: min(int(lch.L*7), 6), h: -1}
		if lch.C >= 0.04 {
			k.h = int(lch.H/30) % 12
			k.c = 1
			if lch.C >= 0.1 {
				k.c = 2
			}
		}
		s, ok := sums[k]
		if !ok {
			s = &sum{}
			sums[k] = s
			order = append(order, k)
		}
		s.n++
		s.l += p.l
		s.a += p.a
		s.b += p.b
	}
	out := make([]clusterStat, len(order))
	for i, k := range order {
		s := sums[k]
		n := float64(s.n)
		out[i] = clusterStat{n: s.n, color: lab{s.l / n, s.a / n, s.b / n}.lch()}
	}
	return out
}

// The accent picker, ported from pickAccent in web/src/lib/color.ts.
const (
	buckets   = 24 // 15° each
	minChroma = 0.04
	minScore  = 0.002
)

// pickAccent picks the dominant vivid hue. Pixels count by chroma and by
// how far their lightness is from black or white, so a splash of color
// beats a large gray background. It's nil for grayscale art.
func pickAccent(px []lab) *Accent {
	var weight, sinSum, cosSum, chromaSum [buckets]float64
	for _, p := range px {
		c := p.lch()
		if c.C < minChroma {
			continue
		}
		w := c.C * math.Max(0, 1-math.Abs(c.L-0.6)/0.45)
		if w <= 0 {
			continue
		}
		b := int(c.H/(360/buckets)) % buckets
		rad := c.H * math.Pi / 180
		weight[b] += w
		sinSum[b] += math.Sin(rad) * w
		cosSum[b] += math.Cos(rad) * w
		chromaSum[b] += c.C * w
	}
	if len(px) == 0 {
		return nil
	}
	best, bestScore := -1, 0.0
	for b := range buckets {
		score := weight[b] + 0.5*(weight[(b+1)%buckets]+weight[(b+buckets-1)%buckets])
		if score > bestScore {
			best, bestScore = b, score
		}
	}
	if best < 0 || bestScore/float64(len(px)) < minScore {
		return nil
	}
	var sin, cos, w, chroma float64
	for _, b := range []int{(best + buckets - 1) % buckets, best, (best + 1) % buckets} {
		sin += sinSum[b]
		cos += cosSum[b]
		w += weight[b]
		chroma += chromaSum[b]
	}
	h := math.Atan2(sin, cos) * 180 / math.Pi
	if h < 0 {
		h += 360
	}
	// accentChroma: vivid art doesn't burn, muted art still has some color.
	return &Accent{H: h, C: min(0.19, max(0.07, chroma/w))}
}
