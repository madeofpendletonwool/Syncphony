# ADR 0005: History, stats and recaps

- **Status:** accepted
- **Date:** 2026-10-05
- **Issue:** MAD-703

## Context

`play_history` records every song a room started, when it ended and why. People want to look back: what played, who brought what, and a recap of last night. Phase 6 has the polished "Wrapped" card (MAD-722). This is the data and a plain view of it.

## Decision

### Computed on read, from play_history

There are no stats tables. `internal/stats` is pure, like `fairness`: plays in, numbers out. The API reads the plays in a range and sums them on each request. At party scale, a room's whole history is a few thousand rows. Reads are capped at 10,000 plays, so a years-old room stays cheap. If that ever bites, the summary can be cached per session, because a finished session never changes.

### What counts

- **A play** is a song that finished or was skipped. Songs that failed or were removed don't count anywhere.
- **Listening time** is how long each play lasted, capped at the song's length, because a speaker that never reported the end would otherwise inflate it.
- **Top tracks and artists** count songs that played to the end: a skip isn't a vote for a song.
- **The same song** on two services counts once when both have an ISRC.
- **Artists** match by name, ignoring case.
- **A person's numbers** are about the songs they queued, whoever skipped them.

### Sessions

A session is a stretch of listening with no two-hour gap. Sessions are found on read from play times, so changing the gap needs no migration. A session's recap is just the stats for its time range, plus the songs that opened and closed it. Rooms don't have a "start/end party" button to forget to press.

### History pages and re-queueing

History pages go back by `before` (the last row's `startedAt`), not by offset, so new plays don't shift pages. It can be filtered to one person's songs.

"Add again" queues a played song through the link it was played from. That only works if you can use that link: it's yours, or it's shared with you. Otherwise the button isn't offered. Matching the song on your own service is cross-service matching (MAD-704).
