# ADR 0015: Music party games

- **Status:** accepted
- **Date:** 2026-10-08
- **Issue:** MAD-783 (stage 1: MAD-784, MAD-785, MAD-786, MAD-787)

## Context

A party app lives or dies on what happens between songs. The server already knows a lot about each song: liner notes (first release, covers, samples, credits), synced lyrics, beat maps (sections, energy, tempo), the music graph (similar artists, top songs, tags, popularity) and the room's history. Phase 10 turns that into games about the music. Social deduction ("who queued this?") is out of scope.

## Decision

### Off by default, one level, then fine controls

`rooms.Settings.Games` (settings version 2) holds a level: `off`, `recap`, `ambient`, `rounds` or `gamenight`. Each level sets the defaults for the rest: which games may run and which are on, a round every N songs, who sees scores, and at Game night the breaks per hour. A field left out of a change takes the new level's default, so sending only a level resets the fine controls. `enabled` keeps only the switches that differ from the level's defaults. A new `startRounds` permission (Everyone or Owner, Owner by default) says who may start a round by hand.

The engine reads the settings as each song starts, so a level change applies from the next song and never cuts off a round in progress.

### The server runs every round

`games.Engine` keeps one round per room in memory, like playback state. A round moves through `announce → open → reveal → done`. It starts when the room's frequency says one is due as a song begins, or when someone with the permission starts one. Each move goes out as `game.round` on the room topic. Clients only show rounds and send answers (`POST /rooms/{id}/games/rounds/{roundId}/answers`).

- **Timing.** Answers open on the next section start when the song has a beat map, and close on a section boundary or downbeat, so the reveal lands with the music. Without a beat map they're fixed timers. A lyric question closes when the singer reaches the line. A round is cut short and revealed if its song stops playing.
- **Answers.** The kinds are multiple choice, a number (a year, shown as choices too), free text, and song picks. Free text matches fuzzily: case, punctuation, accents, a leading "the", bracketed asides and a few typos (about one per six letters) don't matter. Each person gets one answer per round and can change it until the round closes. Speed counts from when the server received the latest answer.
- **Scoring.** A right answer scores 500, plus up to 500 more for speed. A near miss on a number scores a share of that. Finished rounds and answers are kept (`game_rounds`, `game_answers`), and the night's scores are everyone's points since the room's last night ended. They go out as `game.scores` after each reveal: everyone's on a `board`, your own when `private`, none when `off`.
- **Hiding the answer.** A question says what it hides until the reveal: the song (title, artists, album, artwork), its liner notes, or its lyrics. The WebSocket sends `nowplaying.updated` and `queue.updated` with a "Mystery song" to everyone but the speaker. The speaker is the connection whose `device` matches the room's player, because its lock screen shows the song anyway. REST snapshots and the item's artwork, lyrics and liner-notes endpoints refuse with `hidden_for_round` too. When a round starts or stops hiding the song, the room is sent its playback and queue again.

Games that pause the music (finish the lyric, name that tune) are only allowed at Game night and count toward the breaks per hour. The engine doesn't start them until stage 3 teaches it to pause and resume. Queue games aren't about one song, so they're never ready from a song's facts.

### The question kit is pure

Package `quiz` takes a song's facts and a pool of nearby wrong answers and returns questions, like `stats` and `fairness`: no I/O, no clock. `Ready` says which games a song can carry. `games.Sources` gathers the facts as songs come up in rooms that play (a few songs ahead, cached in memory), so a round never waits on a service and never starts on a song that can't answer it.

Wrong answers come from the song's neighbourhood:

- **Titles:** top songs by similar artists, and the night's songs.
- **Years:** within twelve of the real one, never adjacent to it or to each other, never in the future.
- **Credits:** producers and writers from the night's other songs, then similar artists.
- **Samples:** songs by the sampled artist's neighbours.

A wrong answer can never also be right: anyone credited on the song is excluded, and so is any song sharing a title with the answer. Without enough wrong answers there's no question. Difficulty is a rough 0–1 from popularity and how close the wrong answers sit.

### Awards are pure too

Package `awards` turns the night's plays, hearts, beat maps, music graph and game scores into at most six awards, with no person winning more than two. The awards are Deepest Cut, Dance Floor MVP, Vibe Killer, Trendsetter, Time Traveler, Tempo Whiplash, Sample Snitch, The Comeback, The Opener, The Closer and Trivia Champ. A tie for the top skips that award. Autopilot never wins. `nights` computes the awards after it keeps the night, from cached facts only, and stores them on the night. A failure leaves the night without awards rather than losing it. Every level but Off gets awards.

## Consequences

- Rounds live in memory: a restart drops a round in progress, but kept rounds and scores survive.
- Hiding is cooperative on clients that already fetched the lyrics or notes before the round began. The server stops new fetches, and the web app hides them while a round asks.
- Facts are only as good as MusicBrainz, LRCLIB and the graph's sources. Songs they don't know simply aren't asked about.
- Stage 1 asks year, liner-notes, sample and beat-the-singer questions through the shared engine and kit. Stages 2–4 add each game's own UI, clips, pausing rounds and queue games.
