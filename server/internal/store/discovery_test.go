// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"database/sql"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

func TestDiscoverySets(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	alice, bob := user(t, s, "alice"), user(t, s, "bob")
	set, err := s.CreateDiscoverySet(ctx, store.CreateDiscoverySetParams{
		ID: store.NewID(), UserID: alice.ID, Name: "Moody post-rock", Explore: 40, Mode: "both", Now: store.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Bob can't see or change alice's set.
	if _, err := s.GetDiscoverySet(ctx, store.GetDiscoverySetParams{ID: set.ID, UserID: bob.ID}); !store.IsNotFound(err) {
		t.Fatalf("bob got alice's set: %v", err)
	}
	if n, err := s.DeleteDiscoverySet(ctx, store.DeleteDiscoverySetParams{ID: set.ID, UserID: bob.ID}); err != nil || n != 0 {
		t.Fatalf("bob deleted alice's set: %d %v", n, err)
	}
	if sets, err := s.ListDiscoverySets(ctx, bob.ID); err != nil || len(sets) != 0 {
		t.Fatalf("bob's sets: %v %v", sets, err)
	}

	// An artist is in a set once, by its key.
	for _, name := range []string{"Slowdive", "Slowdive", "Hum"} {
		if err := s.AddDiscoverySetArtist(ctx, store.AddDiscoverySetArtistParams{
			SetID: set.ID, ArtistKey: name, Name: name, AddedFrom: "manual", AddedAt: store.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	sets, err := s.ListDiscoverySets(ctx, alice.ID)
	if err != nil || len(sets) != 1 || sets[0].Artists != 2 {
		t.Fatalf("alice's sets: %+v %v", sets, err)
	}
	if err := s.AddDiscoverySetArtist(ctx, store.AddDiscoverySetArtistParams{
		SetID: set.ID, ArtistKey: "x", Name: "X", AddedFrom: "psychic", AddedAt: store.Now(),
	}); err == nil {
		t.Error("bad added_from accepted")
	}
	if err := s.UpdateDiscoverySet(ctx, store.UpdateDiscoverySetParams{
		ID: set.ID, UserID: alice.ID, Name: set.Name, Explore: 101, Mode: "both", UpdatedAt: store.Now(),
	}); err == nil {
		t.Error("explore over 100 accepted")
	}
	if err := s.UpdateDiscoverySet(ctx, store.UpdateDiscoverySetParams{
		ID: set.ID, UserID: alice.ID, Name: set.Name, Explore: 50, Mode: "sideways", UpdatedAt: store.Now(),
	}); err == nil {
		t.Error("bad mode accepted")
	}

	// Feedback outlives its set; the set's artists don't.
	if err := s.PutDiscoveryFeedback(ctx, store.PutDiscoveryFeedbackParams{
		ID: store.NewID(), UserID: alice.ID, SetID: sql.NullString{String: set.ID, Valid: true}, Kind: "up",
		SongKey: "hum\x00stars", Title: "Stars", Artist: "Hum", CreatedAt: store.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DeleteDiscoverySet(ctx, store.DeleteDiscoverySetParams{ID: set.ID, UserID: alice.ID}); err != nil || n != 1 {
		t.Fatalf("deleting: %d %v", n, err)
	}
	if as, err := s.ListDiscoverySetArtists(ctx, set.ID); err != nil || len(as) != 0 {
		t.Fatalf("artists left: %v %v", as, err)
	}
	fb, err := s.ListDiscoveryFeedback(ctx, alice.ID)
	if err != nil || len(fb) != 1 || fb[0].SetID.Valid {
		t.Fatalf("feedback after deleting the set: %+v %v", fb, err)
	}
}

func TestDiscoveryFeedbackLatestWins(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	alice, bob := user(t, s, "alice"), user(t, s, "bob")
	put := func(u store.User, kind string) {
		t.Helper()
		if err := s.PutDiscoveryFeedback(ctx, store.PutDiscoveryFeedbackParams{
			ID: store.NewID(), UserID: u.ID, Kind: kind, SongKey: "hum\x00stars", Title: "Stars", Artist: "Hum",
			DeezerID: sql.NullInt64{Int64: 42, Valid: true}, CreatedAt: store.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	put(alice, "up")
	put(alice, "down")
	put(bob, "up")
	fb, err := s.ListDiscoveryFeedback(ctx, alice.ID)
	if err != nil || len(fb) != 1 || fb[0].Kind != "down" || fb[0].DeezerID.Int64 != 42 {
		t.Fatalf("alice's feedback: %+v %v", fb, err)
	}
	if fb, _ := s.ListDiscoveryFeedback(ctx, bob.ID); len(fb) != 1 || fb[0].Kind != "up" {
		t.Fatalf("bob's feedback: %+v", fb)
	}
	if err := s.PutDiscoveryFeedback(ctx, store.PutDiscoveryFeedbackParams{
		ID: store.NewID(), UserID: alice.ID, Kind: "meh", SongKey: "x", Title: "X", Artist: "Y", CreatedAt: store.Now(),
	}); err == nil {
		t.Error("bad kind accepted")
	}
}

func TestDiscoveryBlocksAndWants(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	alice, bob := user(t, s, "alice"), user(t, s, "bob")
	for range 2 {
		if err := s.BlockDiscoveryArtist(ctx, store.BlockDiscoveryArtistParams{UserID: alice.ID, ArtistKey: "nickelback", Name: "Nickelback", CreatedAt: store.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if bs, _ := s.ListDiscoveryBlocks(ctx, alice.ID); len(bs) != 1 {
		t.Fatalf("blocks: %+v", bs)
	}
	if n, _ := s.UnblockDiscoveryArtist(ctx, store.UnblockDiscoveryArtistParams{UserID: bob.ID, ArtistKey: "nickelback"}); n != 0 {
		t.Fatal("bob unblocked alice's block")
	}
	if n, _ := s.UnblockDiscoveryArtist(ctx, store.UnblockDiscoveryArtistParams{UserID: alice.ID, ArtistKey: "nickelback"}); n != 1 {
		t.Fatal("alice couldn't unblock")
	}

	want := store.AddWantItemParams{
		UserID: alice.ID, Kind: "track", ItemKey: "hum\x00stars", Artist: "Hum", Title: "Stars", AddedAt: store.Now(),
	}
	for range 2 {
		want.ID = store.NewID()
		if err := s.AddWantItem(ctx, want); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.ListWantItems(ctx, alice.ID)
	if err != nil || len(items) != 1 || items[0].Status != "wanted" {
		t.Fatalf("wants: %+v %v", items, err)
	}
	if n, _ := s.SetWantItemStatus(ctx, store.SetWantItemStatusParams{Status: "got", ID: items[0].ID, UserID: bob.ID}); n != 0 {
		t.Fatal("bob changed alice's want")
	}
	if n, err := s.SetWantItemStatus(ctx, store.SetWantItemStatusParams{
		Status: "got", GotAt: sql.NullTime{Time: store.Now(), Valid: true}, ID: items[0].ID, UserID: alice.ID,
	}); err != nil || n != 1 {
		t.Fatalf("marking got: %d %v", n, err)
	}
	if _, err := s.SetWantItemStatus(ctx, store.SetWantItemStatusParams{Status: "lost", ID: items[0].ID, UserID: alice.ID}); err == nil {
		t.Error("bad status accepted")
	}
}

func TestLibraryIndexAndDeezerSongs(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	alice := user(t, s, "alice")
	l, err := s.CreateServiceLink(ctx, store.CreateServiceLinkParams{
		ID: store.NewID(), UserID: alice.ID, Provider: "navidrome", AccountID: "alice@music", AccountLabel: "alice",
		EncryptedCredentials: []byte("x"), Now: store.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if err := s.PutLibraryIndex(ctx, store.PutLibraryIndexParams{
			LinkID: l.ID, BuiltAt: store.Now(), Songs: int64(10 + i), Data: []byte{byte(i)},
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetLibraryIndex(ctx, l.ID)
	if err != nil || got.Songs != 11 || got.Data[0] != 1 || got.ScannedAt.Valid {
		t.Fatalf("index: %+v %v", got, err)
	}
	// It goes with its link.
	if err := s.DeleteServiceLink(ctx, store.DeleteServiceLinkParams{ID: l.ID, UserID: alice.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetLibraryIndex(ctx, l.ID); !store.IsNotFound(err) {
		t.Fatalf("index outlived its link: %v", err)
	}

	if err := s.PutDeezerSong(ctx, store.PutDeezerSongParams{SongKey: "hum\x00stars", DeezerID: 0, CheckedAt: store.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutDeezerSong(ctx, store.PutDeezerSongParams{SongKey: "hum\x00stars", DeezerID: 7, Album: "You'd Prefer an Astronaut", CheckedAt: store.Now()}); err != nil {
		t.Fatal(err)
	}
	if d, err := s.GetDeezerSong(ctx, "hum\x00stars"); err != nil || d.DeezerID != 7 || d.Album == "" {
		t.Fatalf("deezer song: %+v %v", d, err)
	}
}
