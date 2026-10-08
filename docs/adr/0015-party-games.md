# ADR 0015: Music party games

- **Status:** accepted
- **Date:** 2026-10-08
- **Issue:** MAD-783 (stage 1: MAD-784, MAD-785, MAD-786, MAD-787; stage 2: MAD-788, MAD-789, MAD-790, MAD-791)

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

Games that pause the music (finish the lyric, name that tune) are only allowed at Game night and count toward the breaks per hour. Finish the lyric runs from stage 2; name that tune waits for clips (stage 3). Queue games aren't about one song, so they're never ready from a song's facts.

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

### Stage 2: trivia while the song plays

Each game is still a round kind with its question from `quiz`; stage 2 gives them their own shape.

- **Guess the year** is a slider, not choices. Its ends are loose so they don't give the answer away: from a decade start 10–40 years before the year (never later than 1970) to this year. It asks for the first release, so a remaster doesn't fool it. At the reveal the big screen puts everyone's guess on a timeline with the real year marked. An exact guess scores `ExactBonus` (250) more; otherwise the nearest guess that scored at all scores `ClosestBonus` (100) more.
- **Higher or lower** is guess the year's other topic: is this song older or newer than the one the room played before? The engine remembers each room's previous song and finds its year from the facts cache. Two songs from the same year aren't asked. Streaks come from the night's kept rounds (a wrong answer ends one, sitting a round out doesn't) and go out with the scores; the night's best streak is shown only on a board, since it names someone.
- **Liner-notes trivia** also asks who produced or wrote the song, who played on it, which album it's from, what label put it out, and where the artist is from. Wrong answers come from the night's other liner notes (players, albums, labels, places). Where the artist is from falls back to well-known countries or music cities, whichever the answer is, so it never stands out. An album question hides the song, since the album's on every screen. Each reveal carries a `detail` line from the notes. "Who did it first?" for a cover stays "who wrote the original": MusicBrainz doesn't link a cover to the first recording without another lookup per song.
- **Sample detective** reveals both songs' covers side by side. The other song's cover comes from the Cover Art Archive through its MusicBrainz recording (`GET /rooms/{id}/games/rounds/{roundId}/artwork`, only from the reveal on). Playing a clip of the other song waits for the clip endpoint (stage 3). MusicBrainz doesn't describe the sampled part, so there's no description.
- **Beat the singer** shows a line coming up with one to three words blanked, `LyricLead` (8s) before it's sung, and closes as the singer gets there. It picks lines that come back (a chorus) or sit in the song's loudest section, never the first line, and blanks words worth guessing: always the last one (often the rhyme), never small words. Each right word scores a share. A round about a moment later in the song waits `pending`, seen by nobody, until it's time to show the line; it's dropped if the song changes first. It picks from the next few fair lines, so a round started by hand doesn't keep the room waiting.
- **Finish the lyric** (Game night) stops the music as a line begins, through `playback.Engine.Break`, shows the line before, and plays on from that line at the reveal (`Resume`, which only acts if the song is still the room's and still paused). It needs the hour's break budget (`breaksPerHour`, counted from kept rounds) and an engine that can stop the music. A typed line is right with 85% of its words in order; half or more scores a share.
- **Hiding one line.** Lyric games hide `line`, not all the lyrics: the lyrics endpoint blanks that line, everywhere it's sung, until the reveal, and the web app does the same for lyrics it already had.
- **Matching** evens out how lyrics are sung and typed: "do not" and "don't", "runnin'" and "running", "'cause" and "because" are the same.

## Consequences

- Rounds live in memory: a restart drops a round in progress, but kept rounds and scores survive.
- Hiding is cooperative on clients that already fetched the lyrics or notes before the round began. The server stops new fetches, and the web app hides them while a round asks.
- Facts are only as good as MusicBrainz, LRCLIB and the graph's sources. Songs they don't know simply aren't asked about.
- Stage 1 built the shared engine and kit; stage 2 gave the trivia games their own questions, timing and reveals, and taught the engine to stop the music. Stages 3–4 add clips (name that tune) and queue games.
- Higher or lower needs the previous song's facts, so it only appears when they're cached: songs are worked out a few ahead as they're queued.
