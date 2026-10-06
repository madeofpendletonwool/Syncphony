// SPDX-License-Identifier: AGPL-3.0-only

package palette

import (
	"image"
	"image/color"
	"math"
	"testing"
)

type run struct {
	n       int
	r, g, b uint8
}

func pixels(runs ...run) []lab {
	var out []lab
	for _, r := range runs {
		for range r.n {
			out = append(out, oklab(r.r, r.g, r.b))
		}
	}
	return out
}

func TestOklab(t *testing.T) {
	// Reference values, as web/src/lib/color.test.ts checks them.
	red := oklab(255, 0, 0).lch()
	if math.Abs(red.L-0.628) > 0.001 || math.Abs(red.C-0.2577) > 0.001 || math.Abs(red.H-29.23) > 0.1 {
		t.Errorf("red = %+v", red)
	}
	if blue := oklab(0, 0, 255).lch(); math.Abs(blue.H-264.05) > 0.1 {
		t.Errorf("blue = %+v", blue)
	}
	if gray := oklab(128, 128, 128).lch(); gray.C > 1e-3 {
		t.Errorf("gray = %+v", gray)
	}
}

// The same cases as pickAccent's in web/src/lib/color.test.ts, so the two
// agree.
func TestPickAccent(t *testing.T) {
	a := pickAccent(pixels(run{900, 90, 90, 90}, run{100, 30, 120, 240}))
	if a == nil || a.H < 240 || a.H > 270 {
		t.Errorf("vivid over gray: %+v", a)
	}
	a = pickAccent(pixels(run{300, 200, 150, 140}, run{300, 230, 40, 120}))
	if a == nil || a.C < 0.15 || math.Abs(a.H-oklab(230, 40, 120).lch().H) > 3 {
		t.Errorf("more colorful: %+v", a)
	}
	a = pickAccent(pixels(run{50, 235, 60, 120}, run{50, 240, 70, 90}))
	if a == nil || math.Min(a.H, 360-a.H) > 25 {
		t.Errorf("wrapping hues: %+v", a)
	}
	for name, px := range map[string][]lab{
		"grayscale":       pixels(run{500, 20, 20, 20}, run{500, 240, 240, 240}),
		"empty":           nil,
		"near-black tint": pixels(run{1000, 10, 0, 30}),
	} {
		if a := pickAccent(px); a != nil {
			t.Errorf("%s: %+v", name, a)
		}
	}
	// Chroma is clamped.
	if a := pickAccent(pixels(run{100, 255, 0, 0})); a == nil || a.C != 0.19 {
		t.Errorf("clamped: %+v", a)
	}
}

// art draws horizontal bands of color, top to bottom, in rows out of 100.
func art(bands ...run) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	y := 0
	for _, b := range bands {
		end := y + b.n*2
		for ; y < end; y++ {
			for x := range 200 {
				img.Set(x, y, color.RGBA{b.r, b.g, b.b, 255})
			}
		}
	}
	return img
}

func near(c Color, l, ch, h float64) bool {
	dh := math.Abs(c.H - h)
	dh = math.Min(dh, 360-dh)
	return math.Abs(c.L-l) < 0.06 && math.Abs(c.C-ch) < 0.04 && (ch < 0.03 || dh < 12)
}

func TestExtract(t *testing.T) {
	navy := oklab(20, 30, 70).lch()
	red := oklab(220, 40, 50).lch()
	sand := oklab(170, 150, 120).lch()
	cream := oklab(245, 240, 225).lch()
	p := Extract(art(run{50, 20, 30, 70}, run{15, 220, 40, 50}, run{20, 170, 150, 120}, run{15, 245, 240, 225}))

	if !near(p.Dominant, navy.L, navy.C, navy.H) {
		t.Errorf("dominant = %+v, want navy %+v", p.Dominant, navy)
	}
	if !near(p.Vibrant, red.L, red.C, red.H) {
		t.Errorf("vibrant = %+v, want red %+v", p.Vibrant, red)
	}
	if !near(p.Muted, sand.L, sand.C, sand.H) {
		t.Errorf("muted = %+v, want sand %+v", p.Muted, sand)
	}
	if !near(p.Dark, navy.L, navy.C, navy.H) {
		t.Errorf("dark = %+v, want navy", p.Dark)
	}
	if !near(p.Light, cream.L, cream.C, cream.H) {
		t.Errorf("light = %+v, want cream %+v", p.Light, cream)
	}
	if p.Accent == nil || math.Abs(p.Accent.H-red.H) > 5 {
		t.Errorf("accent = %+v, want red's hue", p.Accent)
	}
}

// Grayscale art still gets every role, all neutral.
func TestExtractGrayscale(t *testing.T) {
	p := Extract(art(run{60, 40, 40, 40}, run{40, 200, 200, 200}))
	if p.Accent != nil {
		t.Errorf("accent = %+v", p.Accent)
	}
	for name, c := range map[string]Color{"dominant": p.Dominant, "vibrant": p.Vibrant, "muted": p.Muted, "dark": p.Dark, "light": p.Light} {
		if c.C > 0.02 || c.L <= 0 {
			t.Errorf("%s = %+v", name, c)
		}
	}
	if p.Dark.L >= 0.35 || p.Light.L <= 0.8 || p.Vibrant.L != 0.65 {
		t.Errorf("roles: %+v", p)
	}
}

// One flat color: the missing roles are derived from it.
func TestExtractDerives(t *testing.T) {
	p := Extract(art(run{100, 60, 140, 90}))
	green := oklab(60, 140, 90).lch()
	if !near(p.Dominant, green.L, green.C, green.H) || !near(p.Vibrant, green.L, green.C, green.H) {
		t.Errorf("dominant %+v, vibrant %+v, want %+v", p.Dominant, p.Vibrant, green)
	}
	for name, c := range map[string]Color{"muted": p.Muted, "dark": p.Dark, "light": p.Light} {
		dh := math.Abs(c.H - p.Accent.H)
		if math.Min(dh, 360-dh) > 1 {
			t.Errorf("%s hue %v, want the accent's %v", name, c.H, p.Accent.H)
		}
	}
	if p.Dark.L != 0.22 || p.Light.L != 0.92 {
		t.Errorf("dark %+v, light %+v", p.Dark, p.Light)
	}
}
