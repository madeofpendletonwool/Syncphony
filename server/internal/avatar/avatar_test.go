// SPDX-License-Identifier: AGPL-3.0-only

package avatar_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/avatar"
)

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestProcessCropsAndShrinks(t *testing.T) {
	// A wide photo: red on the left and right thirds, blue in the middle.
	src := image.NewRGBA(image.Rect(0, 0, 900, 300))
	for y := range 300 {
		for x := range 900 {
			c := color.RGBA{R: 255, A: 255}
			if x >= 300 && x < 600 {
				c = color.RGBA{B: 255, A: 255}
			}
			src.Set(x, y, c)
		}
	}
	data, ct, err := avatar.Process(encodePNG(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if ct != "image/jpeg" {
		t.Fatalf("content type %q, want image/jpeg for an opaque image", ct)
	}
	out, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := out.Bounds(); b.Dx() != avatar.Size || b.Dy() != avatar.Size {
		t.Fatalf("size %v, want %dx%d", b, avatar.Size, avatar.Size)
	}
	// The crop is the centered square: all blue.
	for _, p := range []image.Point{{5, 5}, {128, 128}, {250, 250}} {
		r, _, b, _ := out.At(p.X, p.Y).RGBA()
		if b>>8 < 200 || r>>8 > 60 {
			t.Fatalf("pixel %v isn't blue: r=%d b=%d", p, r>>8, b>>8)
		}
	}
}

func TestProcessKeepsSmallAndTransparent(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 64, 80))
	data, ct, err := avatar.Process(encodePNG(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if ct != "image/png" {
		t.Fatalf("content type %q, want image/png for a transparent image", ct)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 64 || cfg.Height != 64 {
		t.Fatalf("small image was resized to %dx%d, want 64x64", cfg.Width, cfg.Height)
	}
}

func TestProcessRejectsNonImages(t *testing.T) {
	if _, _, err := avatar.Process([]byte("<svg xmlns='http://www.w3.org/2000/svg'/>")); !errors.Is(err, avatar.ErrNotImage) {
		t.Fatalf("svg: %v", err)
	}
	if _, _, err := avatar.Process(nil); !errors.Is(err, avatar.ErrNotImage) {
		t.Fatalf("empty: %v", err)
	}
}

func TestProcessRejectsHugeImages(t *testing.T) {
	// A PNG header claiming 20000x20000 pixels.
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	// IHDR width and height are the 8 bytes after the 8-byte signature,
	// 4-byte length and "IHDR"; its CRC follows the 13-byte chunk.
	copy(b[16:24], []byte{0, 0, 0x4e, 0x20, 0, 0, 0x4e, 0x20})
	binary.BigEndian.PutUint32(b[29:33], crc32.ChecksumIEEE(b[12:29]))
	if _, _, err := avatar.Process(b); !errors.Is(err, avatar.ErrTooLarge) {
		t.Fatalf("huge image: %v", err)
	}
}
