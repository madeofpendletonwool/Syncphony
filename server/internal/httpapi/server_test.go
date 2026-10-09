// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// TestPublicOpsMatchSpec checks publicOps lists exactly the operations the
// spec marks `security: []`.
func TestPublicOpsMatchSpec(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromFile("../../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Security) == 0 {
		t.Fatal("spec has no global security requirement")
	}
	seen := map[string]bool{}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			id := strings.ToUpper(op.OperationID[:1]) + op.OperationID[1:]
			public := op.Security != nil && len(*op.Security) == 0
			if public != publicOps[id] {
				t.Errorf("%s %s (%s): public in spec = %v, in publicOps = %v", method, path, id, public, publicOps[id])
			}
			seen[id] = true
		}
	}
	for id := range publicOps {
		if !seen[id] {
			t.Errorf("publicOps has %q, which isn't in the spec", id)
		}
	}
}

// TestDisplayOpsMatchSpec checks displayOps lists exactly the operations
// the spec lets the display cookie call, and that displayRoom knows the
// room of each that has one.
func TestDisplayOpsMatchSpec(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromFile("../../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			id := strings.ToUpper(op.OperationID[:1]) + op.OperationID[1:]
			display := false
			if op.Security != nil {
				for _, req := range *op.Security {
					if _, ok := req["display"]; ok {
						display = true
					}
				}
			}
			if display != displayOps[id] {
				t.Errorf("%s %s (%s): display in spec = %v, in displayOps = %v", method, path, id, display, displayOps[id])
			}
			if display && strings.Contains(path, "{roomId}") {
				if _, ok := displayRoom(displayRequests[id]); !ok {
					t.Errorf("%s: displayRoom doesn't know its room", id)
				}
			}
		}
	}
}

var displayRequests = map[string]any{
	"GetGameRound":           GetGameRoundRequestObject{},
	"GetGameRoundArtwork":    GetGameRoundArtworkRequestObject{},
	"GetGameScores":          GetGameScoresRequestObject{},
	"GetGameClip":            GetGameClipRequestObject{},
	"GetRoom":                GetRoomRequestObject{},
	"GetQueue":               GetQueueRequestObject{},
	"ControlPlayback":        ControlPlaybackRequestObject{},
	"GetPlayback":            GetPlaybackRequestObject{},
	"GetQueueItemArtwork":    GetQueueItemArtworkRequestObject{},
	"GetQueueItemPalette":    GetQueueItemPaletteRequestObject{},
	"GetQueueItemBeatMap":    GetQueueItemBeatMapRequestObject{},
	"GetQueueItemLyrics":     GetQueueItemLyricsRequestObject{},
	"GetQueueItemLinerNotes": GetQueueItemLinerNotesRequestObject{},
	"GetHearts":              GetHeartsRequestObject{},
	"GetGuestPass":           GetGuestPassRequestObject{},
	"ClaimPlayer":            ClaimPlayerRequestObject{},
	"ReleasePlayer":          ReleasePlayerRequestObject{},
	"ReportPlayback":         ReportPlaybackRequestObject{},
	"StreamQueueItem":        StreamQueueItemRequestObject{},
}

func TestClientIP(t *testing.T) {
	s := &Server{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}
	for _, tc := range []struct {
		remote, xff, want string
	}{
		{"203.0.113.5:1234", "", "203.0.113.5"},
		// Untrusted peers can't spoof X-Forwarded-For.
		{"203.0.113.5:1234", "1.2.3.4", "203.0.113.5"},
		{"10.0.0.2:1234", "198.51.100.7", "198.51.100.7"},
		// The client's own (spoofed) entries sit left of the real one.
		{"10.0.0.2:1234", "1.2.3.4, 198.51.100.7", "198.51.100.7"},
		// Chains of trusted proxies are skipped.
		{"10.0.0.2:1234", "198.51.100.7, 10.0.0.9", "198.51.100.7"},
		{"10.0.0.2:1234", "garbage", "10.0.0.2"},
		{"[::ffff:203.0.113.5]:1234", "", "203.0.113.5"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.remote
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		if got := s.clientIP(r); got != tc.want {
			t.Errorf("remote %s, XFF %q: got %s, want %s", tc.remote, tc.xff, got, tc.want)
		}
	}
}
