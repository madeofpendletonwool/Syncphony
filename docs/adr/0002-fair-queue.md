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

### Changes are serialized per room

`queue.Service.Change` takes a per-room lock and runs the change in a transaction. It then bumps the room's queue version and pushes the new snapshot (items plus `upNext`) over realtime. Add, move and remove go through it. The playback engine (MAD-691) will route its state changes (playing, played, skipped) through it too, so changes never interleave and every change gets its own version.

When adding, the service looks up track metadata on the service *before* taking the lock, so a slow provider doesn't block the room.

## Consequences

- New modes (weighted turns, "at most N in a row", per-user cooldowns) are a new `Policy` plus a value in the `rooms.fairness_mode` CHECK constraint. The queue engine and API don't change.
- The order is recomputed on every snapshot. That's O(items × users), trivial at party scale.
- Fairness depends on `play_history`. Until the playback engine writes it, the order is "who queued first" round robin.
- The per-room lock is in-process. Running more than one server against the same database would need a database-level lock instead.
