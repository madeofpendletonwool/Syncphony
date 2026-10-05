// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetHealth(t *testing.T) {
	srv := httptest.NewServer((&Server{Version: "test"}).Handler())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var h Health
	if err := json.NewDecoder(res.Body).Decode(&h); err != nil {
		t.Fatal(err)
	}
	if h.Status != Ok || h.Version != "test" {
		t.Fatalf("got %+v", h)
	}
}
