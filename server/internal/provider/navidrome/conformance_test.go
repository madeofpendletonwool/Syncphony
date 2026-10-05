// SPDX-License-Identifier: AGPL-3.0-only

package navidrome_test

import (
	"cmp"
	"os"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/navidrome"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/providertest"
)

// TestConformance runs the provider suite against a real server. It's
// skipped unless SYNCPHONY_TEST_NAVIDROME_URL is set; `make test-navidrome`
// runs it against the dev compose Navidrome. For another server, also set
// SYNCPHONY_TEST_NAVIDROME_USERNAME, _PASSWORD, and _QUERY (text that finds
// at least one track and album).
func TestConformance(t *testing.T) {
	base := os.Getenv("SYNCPHONY_TEST_NAVIDROME_URL")
	if base == "" {
		t.Skip("SYNCPHONY_TEST_NAVIDROME_URL not set")
	}
	fields := map[string]string{
		"url":      base,
		"username": cmp.Or(os.Getenv("SYNCPHONY_TEST_NAVIDROME_USERNAME"), "admin"),
		"password": cmp.Or(os.Getenv("SYNCPHONY_TEST_NAVIDROME_PASSWORD"), "syncphony"),
	}
	query := cmp.Or(os.Getenv("SYNCPHONY_TEST_NAVIDROME_QUERY"), "sine")
	p := navidrome.New(navidrome.Options{})

	link := func(t *testing.T) provider.Link {
		t.Helper()
		creds, account, err := p.Linker().Complete(t.Context(), provider.LinkInput{Fields: fields})
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		return provider.Link{ID: "link-1", Account: account, Credentials: creds}
	}
	waitForScan(t, p, link(t), query)

	bad := provider.LinkInput{Fields: map[string]string{"url": base, "username": fields["username"], "password": fields["password"] + "-wrong"}}
	providertest.Run(t, providertest.Harness{
		Provider: p,
		Link:     link,
		Query:    query,
		BadInput: &bad,
		// Expiry would mean changing the account's password; the unit
		// tests cover it with a fake server instead.
	})
}

// waitForScan waits for a freshly started server to index its library.
func waitForScan(t *testing.T, p *navidrome.Provider, link provider.Link, query string) {
	t.Helper()
	sess, err := p.Open(t.Context(), link)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		page, err := sess.Search(t.Context(), provider.SearchQuery{Text: query, Kinds: []provider.EntityKind{provider.KindTrack}, Limit: 1})
		if err == nil && len(page.Tracks) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no tracks for %q after 2 minutes (last error: %v)", query, err)
		}
		time.Sleep(2 * time.Second)
	}
}
