// SPDX-License-Identifier: AGPL-3.0-only

// Package avatar turns an uploaded picture into a profile picture: cropped
// to a centered square, shrunk, and re-encoded, so whatever was uploaded
// (metadata, odd formats, huge photos) never reaches anyone else.
package avatar

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	_ "image/gif" // formats people upload
	"image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // and WebP, which image doesn't read itself
)

const (
	// MaxUpload is the largest upload accepted, in bytes.
	MaxUpload = 10 << 20
	// Size is the width and height of a saved avatar, in pixels: sharp at
	// 3x on the largest avatar the app shows.
	Size = 256
	// maxPixels caps the decoded size, so a small file can't claim to be
	// enormous and exhaust memory.
	maxPixels = 50_000_000
)

// ErrNotImage means the upload isn't a JPEG, PNG, GIF or WebP we can read.
var ErrNotImage = errors.New("not a JPEG, PNG, GIF or WebP image")

// ErrTooLarge means the image has too many pixels.
var ErrTooLarge = errors.New("image is too large")

// Process crops data to a centered square, shrinks it to Size, and encodes
// it: JPEG, or PNG if it has transparency. It returns the image and its
// content type.
func Process(data []byte) ([]byte, string, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", ErrNotImage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", ErrNotImage
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, "", ErrTooLarge
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", ErrNotImage
	}

	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	crop := image.Rect(0, 0, side, side).Add(b.Min).Add(image.Pt((b.Dx()-side)/2, (b.Dy()-side)/2))
	out := min(side, Size)
	dst := image.NewRGBA(image.Rect(0, 0, out, out))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Src, nil)

	var buf bytes.Buffer
	if dst.Opaque() {
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 88}); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/jpeg", nil
	}
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, dst); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/png", nil
}
