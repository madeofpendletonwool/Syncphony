// SPDX-License-Identifier: AGPL-3.0-only

package discover_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/discover"
	"github.com/madeofpendletonwool/syncphony/server/internal/library"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// shelf is a library whose holdings a test changes.
type shelf struct{ o *library.Owned }

func (l *shelf) For(context.Context, string) (*library.Owned, error) { return l.o, nil }

func track(artist, title, album string) provider.Track {
	return provider.Track{Title: title, Artists: []provider.ArtistCredit{{Name: artist}}, Album: provider.AlbumCredit{Title: album}}
}

func (e *env) thumb(t *testing.T, setID, kind string, it discover.Item) discover.Thumbed {
	t.Helper()
	did, err := e.svc.Thumb(t.Context(), e.alice, setID, kind, discover.Song{Title: it.Title, Artist: it.Artist, Album: it.Album, Cover: it.Cover, DeezerID: it.DeezerID})
	if err != nil {
		t.Fatal(err)
	}
	return did
}

func songOf(t *testing.T, items []discover.Item, title string) discover.Item {
	t.Helper()
	i := slices.IndexFunc(items, func(it discover.Item) bool { return it.Title == title })
	if i < 0 {
		t.Fatalf("%q isn't in the feed", title)
	}
	return items[i]
}

func TestWantListGotIt(t *testing.T) {
	e := newEnv(t)
	lib := &shelf{o: library.Holding()}
	e.svc.Library = lib
	set := e.set(t, "Slowdive", "Hum")
	all := e.all(t, set.ID)
	it := songOf(t, all, "Swirlies song 1")

	// A thumbs-up wants the song, from the set, and the feed leaves it out.
	did := e.thumb(t, set.ID, discover.ThumbUp, it)
	if did.Want == nil || did.Want.Status != discover.Wanted || did.Want.SetID.String != set.ID || did.Want.Kind != discover.WantTrack {
		t.Fatalf("thumbs-up want: %+v", did.Want)
	}
	if slices.ContainsFunc(e.all(t, set.ID), func(x discover.Item) bool { return x.Key == it.Key }) {
		t.Error("a wanted song is still in the feed")
	}
	// A release, wanted from Fresh.
	rel, err := e.svc.AddWant(t.Context(), e.alice, discover.Want{Kind: discover.WantRelease, Artist: "Ride", Title: "Interplay", SetID: set.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Nothing is in the library yet: nothing is got.
	if n, err := e.svc.CheckWants(t.Context(), e.alice); err != nil || n != 0 {
		t.Fatalf("got %d before it was there (%v)", n, err)
	}
	// The song and the album turn up in Navidrome: the next index marks
	// them got, and they've landed.
	lib.o = library.Holding(track("Swirlies", "Swirlies Song 1", "Something"), track("Ride", "Monaco", "Interplay (Deluxe Edition)"))
	if n, err := e.svc.CheckWants(t.Context(), e.alice); err != nil || n != 2 {
		t.Fatalf("got %d of 2 (%v)", n, err)
	}
	l, err := e.svc.Wants(t.Context(), e.alice)
	if err != nil {
		t.Fatal(err)
	}
	if l.Landed != 2 {
		t.Errorf("landed %d, want 2", l.Landed)
	}
	for _, w := range l.Items {
		if w.Status != discover.Got || w.GotBy.String != discover.GotAuto || !w.GotAt.Valid {
			t.Errorf("%s: %s by %q", w.Title, w.Status, w.GotBy.String)
		}
	}
	if err := e.svc.SeeLanded(t.Context(), e.alice); err != nil {
		t.Fatal(err)
	}
	if l, _ := e.svc.Wants(t.Context(), e.alice); l.Landed != 0 {
		t.Errorf("still %d landed after seeing them", l.Landed)
	}
	// The set's stats say it paid off.
	sums, _ := e.svc.Sets(t.Context(), e.alice)
	i := slices.IndexFunc(sums, func(s discover.Summary) bool { return s.ID == set.ID })
	if sums[i].Wanted != 2 || sums[i].Got != 2 {
		t.Errorf("set yielded %d wanted, %d got; want 2 and 2", sums[i].Wanted, sums[i].Got)
	}

	// Saying you got it doesn't count as landed; dismissing, then wanting
	// again, wants it again.
	other, _ := e.svc.AddWant(t.Context(), e.alice, discover.Want{Kind: discover.WantTrack, Artist: "Lush", Title: "Sweetness and Light"})
	if got, err := e.svc.SetWant(t.Context(), e.alice, other.ID, discover.Got); err != nil || got.GotBy.String != discover.GotManual {
		t.Fatalf("manual got: %+v %v", got, err)
	}
	if l, _ := e.svc.Wants(t.Context(), e.alice); l.Landed != 0 {
		t.Errorf("a manual got landed")
	}
	if _, err := e.svc.SetWant(t.Context(), e.alice, rel.ID, discover.Dismissed); err != nil {
		t.Fatal(err)
	}
	again, err := e.svc.AddWant(t.Context(), e.alice, discover.Want{Kind: discover.WantRelease, Artist: "ride", Title: "Interplay"})
	if err != nil || again.ID != rel.ID || again.Status != discover.Wanted {
		t.Errorf("wanting a dismissed release again: %+v %v", again, err)
	}
	// Bob can't touch alice's list.
	if _, err := e.svc.SetWant(t.Context(), e.bob, rel.ID, discover.Got); !errors.Is(err, discover.ErrNotFound) {
		t.Errorf("bob marked alice's want: %v", err)
	}
	if _, err := e.svc.SetWant(t.Context(), e.alice, rel.ID, "maybe"); err == nil {
		t.Error("a status that isn't one")
	}
}

func TestThumbsGrowASet(t *testing.T) {
	e := newEnv(t)
	set := e.set(t, "Slowdive", "Hum")
	all := e.all(t, set.ID)

	// One thumbs-up suggests nothing; two do.
	e.thumb(t, set.ID, discover.ThumbUp, songOf(t, all, "Swirlies song 1"))
	if sg, _ := e.svc.Suggestions(t.Context(), e.alice, set.ID); len(sg.Grow) != 0 {
		t.Fatalf("suggested after one thumb: %+v", sg.Grow)
	}
	e.thumb(t, set.ID, discover.ThumbUp, songOf(t, all, "Swirlies song 2"))
	sg, err := e.svc.Suggestions(t.Context(), e.alice, set.ID)
	if err != nil || len(sg.Grow) != 1 || sg.Grow[0].Name != "Swirlies" || sg.Grow[0].Thumbs != 2 {
		t.Fatalf("suggestions: %+v %v", sg, err)
	}
	// Thumbs from another set's feed don't count for this one.
	other := e.set(t, "Duster")
	if sg, _ := e.svc.Suggestions(t.Context(), e.alice, other.ID); len(sg.Grow) != 0 {
		t.Errorf("another set's thumbs suggested %+v", sg.Grow)
	}
	// Saying no keeps it from coming back.
	if err := e.svc.Dismiss(t.Context(), e.alice, set.ID, discover.SuggestGrow, "Swirlies"); err != nil {
		t.Fatal(err)
	}
	if sg, _ := e.svc.Suggestions(t.Context(), e.alice, set.ID); len(sg.Grow) != 0 {
		t.Errorf("a dismissed suggestion: %+v", sg.Grow)
	}

	// A set that grows by itself adds an artist at their second thumbs-up.
	on := true
	if _, err := e.svc.Update(t.Context(), e.alice, set.ID, discover.Changes{GrowAuto: &on}); err != nil {
		t.Fatal(err)
	}
	if did := e.thumb(t, set.ID, discover.ThumbUp, songOf(t, all, "Ride song 1")); did.Grew != "" {
		t.Fatalf("grew after one thumb: %s", did.Grew)
	}
	if did := e.thumb(t, set.ID, discover.ThumbUp, songOf(t, all, "Ride song 2")); did.Grew != "Ride" {
		t.Fatalf("grew %q, want Ride", did.Grew)
	}
	got, _ := e.svc.Get(t.Context(), e.alice, set.ID)
	i := slices.IndexFunc(got.Artists, func(a store.DiscoverySetArtist) bool { return a.Name == "Ride" })
	if i < 0 || got.Artists[i].AddedFrom != discover.FromThumb {
		t.Errorf("Ride in the set: %+v", got.Artists)
	}
	if got.GrowAuto != 1 || got.Name != set.Name || got.Explore != set.Explore {
		t.Errorf("turning grow on changed the set: %+v", got.DiscoverySet)
	}
}

func TestThumbsDownLowerAnArtist(t *testing.T) {
	e := newEnv(t)
	set := e.set(t, "Slowdive", "Hum")
	all := e.all(t, set.ID)
	before := songOf(t, all, "Swirlies song 3").Score
	keep := songOf(t, all, "Ride song 1").Score

	e.thumb(t, set.ID, discover.ThumbDown, songOf(t, all, "Swirlies song 1"))
	e.thumb(t, set.ID, discover.ThumbDown, songOf(t, all, "Swirlies song 2"))
	if _, err := e.svc.Feed(t.Context(), e.alice, set.ID, "", true); err != nil {
		t.Fatal(err)
	}
	all = e.all(t, set.ID)
	if after := songOf(t, all, "Swirlies song 3").Score; after >= before*0.5 {
		t.Errorf("Swirlies scored %.3f after two thumbs-down, from %.3f", after, before)
	}
	if after := songOf(t, all, "Ride song 1").Score; after != keep {
		t.Errorf("Ride's songs moved too: %.3f from %.3f", after, keep)
	}
	// A third thumbs-down suggests blocking them, in any set.
	if sg, _ := e.svc.Suggestions(t.Context(), e.alice, set.ID); len(sg.Block) != 0 {
		t.Fatalf("suggested a block after two: %+v", sg.Block)
	}
	e.thumb(t, "", discover.ThumbDown, songOf(t, all, "Swirlies song 3"))
	sg, _ := e.svc.Suggestions(t.Context(), e.alice, set.ID)
	if len(sg.Block) != 1 || sg.Block[0].Name != "Swirlies" || sg.Block[0].Thumbs != 3 {
		t.Fatalf("block suggestions: %+v", sg.Block)
	}
	if _, err := e.svc.Block(t.Context(), e.alice, "Swirlies"); err != nil {
		t.Fatal(err)
	}
	if sg, _ := e.svc.Suggestions(t.Context(), e.alice, set.ID); len(sg.Block) != 0 {
		t.Errorf("still suggests blocking someone blocked: %+v", sg.Block)
	}
}

// stars stands in for what alice starred on Navidrome.
type stars struct{ st provider.Starred }

func (s stars) Starred(context.Context, string) (provider.Starred, error) { return s.st, nil }

func TestImportStarred(t *testing.T) {
	e := newEnv(t)
	e.svc.Stars = stars{provider.Starred{
		Artists: []provider.Artist{{Name: "Low"}},
		Albums: []provider.Album{
			{Title: "Souvlaki", Artists: []provider.ArtistCredit{{Name: "Slowdive"}}},
			{Title: "Pygmalion", Artists: []provider.ArtistCredit{{Name: "Slowdive"}}},
		},
		Songs: []provider.Track{track("Codeine", "D", "Frigid Stars"), track("Slowdive", "Alison", "Souvlaki"), track("The Cure", "Pictures", "Disintegration")},
	}}
	got, err := e.svc.Starred(t.Context(), e.alice)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range got {
		names = append(names, a.Name)
	}
	if want := []string{"Low", "Slowdive", "Codeine", "The Cure"}; !slices.Equal(names, want) {
		t.Fatalf("starred %v, want %v", names, want)
	}
	if got[1].Albums != 2 || got[1].Songs != 1 || got[1].Artist {
		t.Errorf("Slowdive: %+v", got[1])
	}

	// Only the ones picked join, as starred; ones there already are skipped.
	set := e.set(t, "Slowdive")
	set, err = e.svc.Import(t.Context(), e.alice, set.ID, discover.FromStarred, []discover.Seed{{Name: "Slowdive"}, {Name: "Codeine"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Artists) != 2 || set.Artists[1].Name != "Codeine" || set.Artists[1].AddedFrom != discover.FromStarred {
		t.Errorf("after import: %+v", set.Artists)
	}
	// More than fit is too many, and adds none.
	var lots []discover.Seed
	for i := range discover.MaxArtists {
		lots = append(lots, discover.Seed{Name: "Artist " + string(rune('A'+i%26)) + string(rune('a'+i/26))})
	}
	if _, err := e.svc.Import(t.Context(), e.alice, set.ID, discover.FromStarred, lots); !errors.Is(err, discover.ErrTooMany) {
		t.Errorf("importing too many: %v", err)
	}
	if s, _ := e.svc.Get(t.Context(), e.alice, set.ID); len(s.Artists) != 2 {
		t.Errorf("a refused import added %d", len(s.Artists)-2)
	}
	if _, err := e.svc.Import(t.Context(), e.alice, set.ID, "spotify", nil); err == nil {
		t.Error("imported from somewhere that isn't")
	}
}

// room makes a room of bob's with a taste over past nights: Failure and
// Codeine (bob's), and Bedhead (alice's).
func (e *env) room(t *testing.T, visibility string) store.Room {
	t.Helper()
	ctx := t.Context()
	r, err := e.db.CreateRoom(ctx, store.CreateRoomParams{ID: store.NewID(), Name: "Den", OwnerID: e.bob, FairnessMode: "round_robin", Settings: "{}", CreatedAt: store.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.SetRoomVisibility(ctx, store.SetRoomVisibilityParams{Visibility: visibility, ID: r.ID}); err != nil {
		t.Fatal(err)
	}
	for _, a := range []struct {
		member, name string
		likes        float64
	}{{e.bob, "Failure", 6}, {e.bob, "Codeine", 3}, {e.alice, "Bedhead", 4}} {
		if err := e.db.AddDJAffinity(ctx, store.AddDJAffinityParams{
			RoomID: r.ID, Kind: "artist", Key: a.name, Member: a.member, Name: a.name, Likes: a.likes, LastNight: 1,
			LastAt: sql.NullTime{Time: store.Now(), Valid: true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (e *env) roomSource() *discover.RoomSource {
	rs := &discover.RoomSource{DB: e.db}
	rs.CanEnter = func(ctx context.Context, userID string, r store.Room) (bool, error) {
		if r.Visibility == store.VisibilityOpen || r.OwnerID == userID {
			return true, nil
		}
		_, err := e.db.GetRoomMember(ctx, store.GetRoomMemberParams{RoomID: r.ID, UserID: userID})
		return err == nil, nil
	}
	return rs
}

func TestRoomTaste(t *testing.T) {
	e := newEnv(t)
	e.svc.Rooms = e.roomSource()
	open := e.room(t, store.VisibilityOpen)

	taste, err := e.svc.RoomTaste(t.Context(), e.alice, open.ID, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range taste {
		names = append(names, a.Name)
	}
	// Each member's likes count the same: alice's one artist is as liked
	// as bob's favorite.
	if len(names) != 3 || names[2] != "Codeine" || !slices.Contains(names, "Bedhead") || !slices.Contains(names, "Failure") {
		t.Fatalf("taste %v", names)
	}
	mine, err := e.svc.RoomTaste(t.Context(), e.alice, open.ID, "", true)
	if err != nil || len(mine) != 1 || mine[0].Name != "Bedhead" {
		t.Fatalf("alice's own picks: %+v %v", mine, err)
	}

	// Reading it changes nothing of the room's.
	room, err := e.db.GetDJRoom(t.Context(), open.ID)
	if !store.IsNotFound(err) {
		t.Errorf("reading the taste wrote the room's DJ memory: %+v %v", room, err)
	}

	// A private room alice isn't in is not found, to link or to read.
	closed := e.room(t, store.VisibilityPrivate)
	if _, err := e.svc.RoomTaste(t.Context(), e.alice, closed.ID, "", false); !errors.Is(err, discover.ErrRoomNotFound) {
		t.Errorf("read a room alice can't open: %v", err)
	}
	set := e.set(t, "Slowdive")
	if _, err := e.svc.Update(t.Context(), e.alice, set.ID, discover.Changes{RoomID: &closed.ID}); !errors.Is(err, discover.ErrRoomNotFound) {
		t.Errorf("linked a room alice can't open: %v", err)
	}
}

func TestLinkedRoomShiftsTheFeed(t *testing.T) {
	e := newEnv(t)
	e.svc.Rooms = e.roomSource()
	r := e.room(t, store.VisibilityOpen)
	// Failure's neighbors, which none of the seeds lead to.
	e.graph.artist("Failure", 100_000, "Hum", 0.6, "Jawbox", 0.8, "Shiner", 0.5)
	e.graph.artist("Jawbox", 100_000)
	set := e.set(t, "Slowdive")
	has := func(artist string) bool {
		return slices.ContainsFunc(e.all(t, set.ID), func(it discover.Item) bool { return it.Artist == artist })
	}
	if has("Jawbox") {
		t.Fatal("Jawbox before the room was linked")
	}
	weight := discover.RoomHigh
	set, err := e.svc.Update(t.Context(), e.alice, set.ID, discover.Changes{RoomID: &r.ID, RoomWeight: &weight})
	if err != nil {
		t.Fatal(err)
	}
	if !set.RoomID.Valid || set.RoomWeight != discover.RoomHigh {
		t.Fatalf("linked: %+v", set.DiscoverySet)
	}
	if !has("Jawbox") {
		t.Error("the room's taste didn't reach the feed")
	}
	// Unlinking takes it out again.
	none := ""
	if _, err := e.svc.Update(t.Context(), e.alice, set.ID, discover.Changes{RoomID: &none}); err != nil {
		t.Fatal(err)
	}
	if has("Jawbox") {
		t.Error("still walking from the room after unlinking it")
	}
	bad := "loud"
	if _, err := e.svc.Update(t.Context(), e.alice, set.ID, discover.Changes{RoomWeight: &bad}); err == nil {
		t.Error("a weight that isn't one")
	}
}
