# ADR 0002: Fair queue

- **Status:** accepted
- **Date:** 2026-10-05
- **Issue:** MAD-690

## Context

Syncphony's point is that everyone at the party gets heard. A plain shared queue rewards whoever adds the most songs first. We want turns: each person queues as much as they like, and the room takes one song from each person in turn.

## Decision

### Lanes

Each member has a **lane**: the songs they've queued in a room, in the order they choose. They can reorder and remove items in their own lane. The room owner can remove anyone's songs, but can't reorder other people's lanes. Lanes are stored as `queue_items` with a sparse `lane_position`.

### Play order is computed, not stored

The "up next" order is a pure function of the lanes, who is playing, and when each person last had a song start (`play_history`). It lives in `internal/fairness`, has no I/O and no clock, and is recomputed for every queue snapshot. Nothing about the order is persisted, so changing the rule, or a room's mode, takes effect at once and needs no migration.

```go
type Policy interface {
    Mode() string               // the room's fairness_mode
    Order(State) []Item         // every queued item, in play order
}
```

A policy must be deterministic, keep each lane's order, and return every item exactly once. A property test checks this against random input for every policy.

### Round robin (the default)

The next turn goes to whoever **waited longest since their last song started**:

1. People who haven't had a song played in this room go first, ordered by when they started waiting (their earliest queued item), then by user ID.
2. Then everyone else, by when their last song started.
3. Whoever is playing right now counts as having just had their turn.

The order is simulated turn by turn, so each pick moves that person to the back.

This means someone who arrives late plays soon instead of after every earlier lane drains, and someone whose lane ran dry keeps their place in line when they add more. A skipped song still used a turn, because it started.

### FIFO

`fifo` plays songs in the order they were added, across everyone. Lanes still apply: it merges them by when each lane's next item was added, so reordering your own lane still works.

### Tuning: weights, caps, cooldowns (MAD-702)

A room tunes its mode with options in `rooms.settings` (`fairness`). They're `fairness.Options`, passed to the policy alongside the mode:

- **Weights** (round robin only): someone with weight 2 gets two songs each time their turn comes, back to back. Everyone else gets 1.
- **Max in a row**: nobody gets more than N songs in a row. This mostly matters for FIFO, where one person adding an album would otherwise take over.
- **Cooldown**: at least N other songs play between one person's songs.

The cap and the cooldown only hold back someone while somebody else has songs waiting. If they'd hold back everyone, the order goes on as if they weren't set: the music never waits on a rule. The cap can cut a weighted turn short.

Both policies now run the same step-by-step simulation, which knows who played the last few songs. `State.Recent` carries that history from `play_history`, as far back as the options need (`Options.Reach`), so a cap or a weighted turn picks up where the room actually is. The playing song counts.

The simulation is still a pure function, and the property tests run with options on as well as off.

### Repeat guard

`repeatWindowMinutes` refuses a song that's already waiting or playing, or started within the window. A song counts as the same if it's the same track on the same service, or has the same ISRC on any service. This is a check when songs are added, in `queue.Add`, not an ordering rule. A multi-song add (an album) leaves the repeats out. If nothing is left to add, the add is refused with `repeat`.

### Changes are serialized per room

`queue.Service.Change` takes a per-room lock and runs the change in a transaction. It then bumps the room's queue version and pushes the new snapshot (items plus `upNext`) over realtime. Add, move and remove go through it. The playback engine (MAD-691) will route its state changes (playing, played, skipped) through it too, so changes never interleave and every change gets its own version.

When adding, the service looks up track metadata on the service *before* taking the lock, so a slow provider doesn't block the room.

## Consequences

- A new mode is a new `Policy` plus a value in the `rooms.fairness_mode` CHECK constraint. Tuning a mode is an `Options` field. In neither case does the queue engine change.
- A time-based cooldown ("one song per person per 10 minutes") was left out. It would put a clock into the order, and with lanes nobody needs to be rate limited on adding.
- The order is recomputed on every snapshot. That's O(items × users), trivial at party scale.
- Fairness depends on `play_history`. Until the playback engine writes it, the order is "who queued first" round robin.
- The per-room lock is in-process. Running more than one server against the same database would need a database-level lock instead.
