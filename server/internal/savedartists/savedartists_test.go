// SPDX-License-Identifier: AGPL-3.0-only

package savedartists_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/savedartists"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// pictures is a music graph that knows the pictures in known.
type pictures struct {
	known  map[string]string
	warmed []string
}

func (p *pictures) CachedArtist(_ context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, bool, error) {
	pic, ok := p.known[a.Name]
	return musicgraph.Artist{Ref: a, Picture: pic}, ok, nil
}

func (p *pictures) WarmArtist(as ...musicgraph.ArtistRef) {
	for _, a := range as {
		p.warmed = append(p.warmed, a.Name)
	}
}

func open(t *testing.T) (*store.Store, string) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "syncphony.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	u, err := db.CreateUser(t.Context(), store.CreateUserParams{ID: store.NewID(), Username: "alice", DisplayName: "Alice", Color: "#000", Role: store.RoleMember, CreatedAt: store.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return db, u.ID
}

func TestPictures(t *testing.T) {
	db, alice := open(t)
	pics := &pictures{known: map[string]string{"Hum": "abc"}}
	s := savedartists.New(db, nil, pics)

	hum, _, err := s.Save(t.Context(), alice, savedartists.Input{Name: "Hum"})
	if err != nil || hum.Picture.String != "abc" {
		t.Fatalf("Save(Hum) = %+v, %v", hum, err)
	}
	// Not known yet: it's fetched in the background, and filled in once it is.
	if _, _, err := s.Save(t.Context(), alice, savedartists.Input{Name: "Failure"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pics.warmed, []string{"Failure"}) {
		t.Errorf("warmed %v", pics.warmed)
	}
	pics.known["Failure"] = "def"
	list, err := s.List(t.Context(), alice)
	if err != nil || len(list) != 2 || list[0].Name != "Failure" || list[0].Picture.String != "def" {
		t.Fatalf("List = %+v, %v", list, err)
	}
}

func TestRefsAreCapped(t *testing.T) {
	db, alice := open(t)
	s := savedartists.New(db, nil, nil)
	var a savedartists.Artist
	for i := range 12 {
		var err error
		a, _, err = s.Save(t.Context(), alice, savedartists.Input{Name: "Hum", LinkID: "l", ArtistID: string(rune('a' + i))})
		if err != nil {
			t.Fatal(err)
		}
	}
	// The newest, the twelfth (ID "l"), comes first.
	if len(a.Refs) != 10 || a.Refs[0].ArtistID != "l" {
		t.Errorf("refs %+v", a.Refs)
	}
}
