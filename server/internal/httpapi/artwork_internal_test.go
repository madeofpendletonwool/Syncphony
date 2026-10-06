// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/artcache"
)

func TestSmallerThan(t *testing.T) {
	img := func(w int) artcache.Image {
		var b bytes.Buffer
		if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, 1))); err != nil {
			t.Fatal(err)
		}
		return artcache.Image{Data: b.Bytes(), ContentType: "image/png"}
	}
	for _, tc := range []struct {
		img  artcache.Image
		px   int
		want bool
	}{
		{img(300), 600, true},
		{img(600), 600, false},
		{img(640), 1200, true},
		{img(300), 0, true}, // the default is 600
		{img(800), 0, false},
		{artcache.Image{Data: []byte("<svg/>"), ContentType: "image/svg+xml"}, 2048, false},
	} {
		if got := smallerThan(tc.img, tc.px); got != tc.want {
			t.Errorf("%d px wide, want %d: got %v", width(tc.img), tc.px, got)
		}
	}
}
