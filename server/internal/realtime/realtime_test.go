// SPDX-License-Identifier: AGPL-3.0-only

package realtime

import (
	"sync"
	"testing"
)

func TestPublishSubscribe(t *testing.T) {
	b := NewLocal()
	room := b.Subscribe(RoomTopic("r1"))
	both := b.Subscribe(RoomTopic("r1"), UserTopic("u1"))
	other := b.Subscribe(RoomTopic("r2"))

	b.Publish(RoomTopic("r1"), Event{Type: QueueUpdated, Version: 1})
	b.Publish(UserTopic("u1"), Event{Type: LinkStatus})

	if e := <-room.C; e.Type != QueueUpdated || e.Version != 1 {
		t.Fatalf("room got %+v", e)
	}
	if e1, e2 := <-both.C, <-both.C; e1.Type != QueueUpdated || e2.Type != LinkStatus {
		t.Fatalf("both got %+v, %+v", e1, e2)
	}
	select {
	case e := <-other.C:
		t.Fatalf("other room got %+v", e)
	default:
	}

	both.Close()
	both.Close() // idempotent
	if _, ok := <-both.C; ok {
		t.Fatal("closed subscription still open")
	}
	if both.Lagged() {
		t.Fatal("closed subscription reports lag")
	}
	b.Publish(UserTopic("u1"), Event{Type: LinkStatus}) // no subscribers left; mustn't panic
	if len(b.subs[UserTopic("u1")]) != 0 {
		t.Fatal("closed subscription still registered")
	}
}

func TestSlowSubscriberIsCutOff(t *testing.T) {
	b := NewLocal()
	slow := b.Subscribe("t")
	fast := b.Subscribe("t")
	for i := range subscriptionBuffer + 1 {
		b.Publish("t", Event{Version: int64(i)})
		<-fast.C
	}
	n := 0
	for range slow.C {
		n++
	}
	if n != subscriptionBuffer || !slow.Lagged() {
		t.Fatalf("slow subscriber: drained %d, lagged %v", n, slow.Lagged())
	}
	if fast.Lagged() {
		t.Fatal("fast subscriber cut off")
	}
}

func TestConcurrent(_ *testing.T) {
	b := NewLocal()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			s := b.Subscribe("t")
			for range 100 {
				b.Publish("t", Event{})
			}
			s.Close()
		})
	}
	wg.Wait()
}

func TestPresence(t *testing.T) {
	p := NewPresence()
	if !p.Join("r", "alice") {
		t.Fatal("first connection isn't first")
	}
	if p.Join("r", "alice") {
		t.Fatal("second connection is first")
	}
	p.Join("r", "bob")
	if m := p.Members("r"); len(m) != 2 {
		t.Fatalf("members %v", m)
	}
	if p.Leave("r", "alice") {
		t.Fatal("alice left with a connection still open")
	}
	if !p.Leave("r", "alice") {
		t.Fatal("alice's last connection didn't count as leaving")
	}
	if p.Leave("r", "alice") {
		t.Fatal("leaving twice")
	}
	if m := p.Members("r"); len(m) != 1 || m[0] != "bob" {
		t.Fatalf("members %v", m)
	}
}
