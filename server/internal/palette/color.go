// SPDX-License-Identifier: AGPL-3.0-only

package palette

import "math"

// Color is an OKLCH color: lightness 0-1, chroma about 0-0.37, hue in
// degrees. CSS writes it oklch(L C H).
type Color struct {
	L float64 `json:"l"`
	C float64 `json:"c"`
	H float64 `json:"h"`
}

// lab is an OKLab color.
type lab struct{ l, a, b float64 }

func toLinear(v uint8) float64 {
	s := float64(v) / 255
	if s <= 0.04045 {
		return s / 12.92
	}
	return math.Pow((s+0.055)/1.055, 2.4)
}

// oklab converts 8-bit sRGB to OKLab. It mirrors rgbToOklch in
// web/src/lib/color.ts.
func oklab(r, g, b uint8) lab {
	lr, lg, lb := toLinear(r), toLinear(g), toLinear(b)
	l := math.Cbrt(0.4122214708*lr + 0.5363325363*lg + 0.0514459929*lb)
	m := math.Cbrt(0.2119034982*lr + 0.6806995451*lg + 0.1073969566*lb)
	s := math.Cbrt(0.0883024619*lr + 0.2817188376*lg + 0.6299787005*lb)
	return lab{
		l: 0.2104542553*l + 0.793617785*m - 0.0040720468*s,
		a: 1.9779984951*l - 2.428592205*m + 0.4505937099*s,
		b: 0.0259040371*l + 0.7827717662*m - 0.808675766*s,
	}
}

func (c lab) lch() Color {
	ch := math.Hypot(c.a, c.b)
	h := 0.0
	if ch >= 1e-4 {
		h = math.Atan2(c.b, c.a) * 180 / math.Pi
		if h < 0 {
			h += 360
		}
	}
	return Color{L: c.l, C: ch, H: h}
}

// rounded trims a color to what CSS and the eye can tell apart.
func (c Color) rounded() Color {
	r := func(v, unit float64) float64 { return math.Round(v/unit) * unit }
	return Color{L: r(c.L, 0.001), C: r(c.C, 0.0001), H: r(c.H, 0.01)}
}
