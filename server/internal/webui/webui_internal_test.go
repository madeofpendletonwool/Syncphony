// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandler(t *testing.T) {
	h := handler(fstest.MapFS{
		"index.html":           {Data: []byte("<html>shell</html>")},
		"sw.js":                {Data: []byte("// worker")},
		"manifest.webmanifest": {Data: []byte("{}")},
		"icon-192.png":         {Data: []byte("png")},
		"assets/index-abc.js":  {Data: []byte("js")},
	})
	for _, tc := range []struct{ path, cache, body string }{
		{"/", "no-cache", "shell"},
		{"/room", "no-cache", "shell"}, // a client-side route
		{"/sw.js", "no-cache", "worker"},
		{"/manifest.webmanifest", "no-cache", "{}"},
		{"/assets/index-abc.js", "public, max-age=31536000, immutable", "js"},
		{"/icon-192.png", "", "png"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != tc.cache || !strings.Contains(rec.Body.String(), tc.body) {
			t.Errorf("%s: %d %q %q", tc.path, rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
		}
	}
}
