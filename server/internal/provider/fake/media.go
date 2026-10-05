// SPDX-License-Identifier: AGPL-3.0-only

package fake

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"html"
	"math"
	"strings"
	"time"
)

// wav renders a sine tone as 8 kHz, 8-bit mono PCM: small, and playable
// in every browser.
func wav(hz float64, d time.Duration) []byte {
	const rate = 8000
	n := uint32(d.Seconds() * rate)
	b := make([]byte, 44+int(n))
	copy(b[0:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], 36+n)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16) // fmt chunk size
	binary.LittleEndian.PutUint16(b[20:], 1)  // PCM
	binary.LittleEndian.PutUint16(b[22:], 1)  // mono
	binary.LittleEndian.PutUint32(b[24:], rate)
	binary.LittleEndian.PutUint32(b[28:], rate) // byte rate
	binary.LittleEndian.PutUint16(b[32:], 1)    // block align
	binary.LittleEndian.PutUint16(b[34:], 8)    // bits per sample
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], n)
	for i := range int(n) {
		b[44+i] = byte(128 + 48*math.Sin(2*math.Pi*hz*float64(i)/rate))
	}
	return b
}

// artworkSVG draws a square in a color derived from id, with label's initials.
func artworkSVG(id, label string) []byte {
	h := fnv.New32a()
	h.Write([]byte(id))
	hue := h.Sum32() % 360
	var initials strings.Builder
	for w := range strings.FieldsSeq(label) {
		if initials.Len() < 2 {
			initials.WriteString(strings.ToUpper(w[:1]))
		}
	}
	return fmt.Appendf(nil, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 300 300">`+
		`<rect width="300" height="300" fill="hsl(%d 60%% 45%%)"/>`+
		`<text x="150" y="150" dy=".35em" text-anchor="middle" font-family="sans-serif" font-size="120" fill="white">%s</text>`+
		`</svg>`, hue, html.EscapeString(initials.String()))
}
