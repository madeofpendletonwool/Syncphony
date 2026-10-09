// SPDX-License-Identifier: AGPL-3.0-only

package rooms

import (
	"fmt"
	"maps"
	"slices"
)

// Games are a room's party games (Phase 10, ADR 0015): off by default. The
// owner picks a level, and each level sets the defaults for the rest. The
// zero value is Off.
type Games struct {
	// Level is one of the Game* levels. Empty means GamesOff.
	Level string `json:"level,omitempty"`
	// Enabled switches single games on or off, by Game kind. A game the
	// level doesn't allow stays off whatever it says; a missing one takes
	// the level's default.
	Enabled map[string]bool `json:"enabled,omitempty"`
	// Frequency is a round about every this many songs, 0 for only when
	// someone starts one, up to MaxGameFrequency. Nil is the level's.
	Frequency *int `json:"frequency,omitempty"`
	// NoGuests stops guests answering. Stored negated so guests play by
	// default. It's separate from guests' hearts and votes.
	NoGuests bool `json:"noGuests,omitempty"`
	// Scores is ScoresOff, ScoresPrivate or ScoresBoard. Empty is the level's.
	Scores string `json:"scores,omitempty"`
	// TVOnly shows rounds on the big screen only, never as prompts on phones.
	TVOnly bool `json:"tvOnly,omitempty"`
	// BreaksPerHour is, at Game night, the most rounds an hour that may
	// pause the music: 0 to MaxBreaksPerHour. Nil is DefaultBreaksPerHour.
	BreaksPerHour *int `json:"breaksPerHour,omitempty"`
	// Tune sets up name that tune. Nil takes the defaults.
	Tune *Tune `json:"tune,omitempty"`
}

// Tune sets up name that tune (MAD-793).
type Tune struct {
	// From is where the tunes come from: TuneTonight, TuneFavorites or
	// TuneNew. Empty is TuneTonight.
	From string `json:"from,omitempty"`
	// Typed is hard mode: type the title instead of picking it.
	Typed bool `json:"typed,omitempty"`
	// Clip is where in the song the clips come from: ClipChorus, ClipIntro
	// or ClipOutro. Empty is ClipChorus.
	Clip string `json:"clip,omitempty"`
}

// Where name that tune's songs come from.
const (
	// TuneTonight: songs the room played tonight.
	TuneTonight = "tonight"
	// TuneFavorites: the room's favorites over every night.
	TuneFavorites = "favorites"
	// TuneNew: songs the room has never played, by artists it likes,
	// from the DJ's candidates.
	TuneNew = "new"
)

// TuneSources are where tunes may come from, in order.
var TuneSources = []string{TuneTonight, TuneFavorites, TuneNew}

// Where in a song name that tune's clips come from.
const (
	// ClipChorus: the song's loudest section, usually a chorus.
	ClipChorus = "chorus"
	// ClipIntro: its first seconds.
	ClipIntro = "intro"
	// ClipOutro: its last seconds.
	ClipOutro = "outro"
)

// ClipSpots are where clips may come from, in order.
var ClipSpots = []string{ClipChorus, ClipIntro, ClipOutro}

// TuneOf is name that tune's setup, with the defaults filled in.
func (g Games) TuneOf() Tune {
	var t Tune
	if g.Tune != nil {
		t = *g.Tune
	}
	if t.From == "" {
		t.From = TuneTonight
	}
	if t.Clip == "" {
		t.Clip = ClipChorus
	}
	return t
}

// Game levels, from nothing to a full game night.
const (
	// GamesOff: nothing.
	GamesOff = "off"
	// GamesRecap: awards when the night ends, and nothing else.
	GamesRecap = "recap"
	// GamesAmbient: little questions you can ignore. The music never stops.
	GamesAmbient = "ambient"
	// GamesRounds: some songs become a round, with a reveal on the big screen.
	GamesRounds = "rounds"
	// GamesNight: dedicated rounds that may pause the music, and queue games.
	GamesNight = "gamenight"
)

// GameLevels are the levels, in order.
var GameLevels = []string{GamesOff, GamesRecap, GamesAmbient, GamesRounds, GamesNight}

// Game kinds: what a round asks about. Each game plugs into the round
// engine (package games) as one.
const (
	// GameYear: guess the year, and higher or lower.
	GameYear = "year"
	// GameLiner: liner-notes trivia, covers, credits and releases.
	GameLiner = "liner"
	// GameSample: sample detective.
	GameSample = "sample"
	// GameLyrics: beat the singer to the next line.
	GameLyrics = "lyrics"
	// GameFinishLyric: the music stops, and you finish the line.
	GameFinishLyric = "finish_lyric"
	// GameTune: name that tune, from a short clip.
	GameTune = "tune"
	// GameConnect: connect the artists through the queue.
	GameConnect = "connect"
	// GameTheme: theme rounds, where everyone queues to a theme.
	GameTheme = "theme"
	// GameBracket: bracket battles between songs.
	GameBracket = "bracket"
)

// GameKinds are every game, in the order the settings show them.
var GameKinds = []string{GameYear, GameLiner, GameSample, GameLyrics, GameFinishLyric, GameTune, GameConnect, GameTheme, GameBracket}

// GameBreaks are the games that pause the music, so they're Game night's
// only and count toward BreaksPerHour.
var GameBreaks = []string{GameFinishLyric, GameTune}

// Who sees scores.
const (
	ScoresOff     = "off"
	ScoresPrivate = "private"
	ScoresBoard   = "board"
)

// Games limits.
const (
	MaxGameFrequency     = 20
	DefaultBreaksPerHour = 4
	MaxBreaksPerHour     = 12
)

// gameLevel is what a level allows, and its defaults.
type gameLevel struct {
	// allowed are the games the level may run; on, those on by default.
	allowed, on []string
	frequency   int
	scores      string
}

var gameLevels = map[string]gameLevel{
	GamesOff:   {scores: ScoresOff},
	GamesRecap: {scores: ScoresOff},
	GamesAmbient: {
		allowed:   []string{GameYear, GameLiner, GameSample},
		on:        []string{GameYear, GameLiner, GameSample},
		frequency: 2, scores: ScoresPrivate,
	},
	GamesRounds: {
		allowed:   []string{GameYear, GameLiner, GameSample, GameLyrics},
		on:        []string{GameYear, GameLiner, GameSample, GameLyrics},
		frequency: 3, scores: ScoresBoard,
	},
	GamesNight: {
		allowed:   GameKinds,
		on:        GameKinds,
		frequency: 2, scores: ScoresBoard,
	},
}

// LevelOf is the room's level, GamesOff if it's not one.
func (g Games) LevelOf() string {
	if _, ok := gameLevels[g.Level]; ok {
		return g.Level
	}
	return GamesOff
}

// Allowed reports whether the level lets a game run at all.
func (g Games) Allowed(kind string) bool {
	return slices.Contains(gameLevels[g.LevelOf()].allowed, kind)
}

// On reports whether a game is on: the level allows it, and it's switched
// on, or left at a level that has it on.
func (g Games) On(kind string) bool {
	if !g.Allowed(kind) {
		return false
	}
	if on, ok := g.Enabled[kind]; ok {
		return on
	}
	return slices.Contains(gameLevels[g.LevelOf()].on, kind)
}

// Kinds are the games that are on, in GameKinds order.
func (g Games) Kinds() []string {
	var out []string
	for _, k := range GameKinds {
		if g.On(k) {
			out = append(out, k)
		}
	}
	return out
}

// Every is how many songs go by between rounds the engine starts by
// itself: 0 for none, only when someone starts one.
func (g Games) Every() int {
	if g.Frequency != nil {
		return *g.Frequency
	}
	return gameLevels[g.LevelOf()].frequency
}

// ScoreMode is who sees scores.
func (g Games) ScoreMode() string {
	if g.Scores != "" && g.Plays() {
		return g.Scores
	}
	return gameLevels[g.LevelOf()].scores
}

// GuestsAnswer reports whether guests may answer.
func (g Games) GuestsAnswer() bool { return !g.NoGuests }

// Breaks is the most rounds an hour that may pause the music: none below
// Game night.
func (g Games) Breaks() int {
	if g.LevelOf() != GamesNight {
		return 0
	}
	if g.BreaksPerHour != nil {
		return *g.BreaksPerHour
	}
	return DefaultBreaksPerHour
}

// Plays reports whether the level runs rounds at all.
func (g Games) Plays() bool {
	l := g.LevelOf()
	return l == GamesAmbient || l == GamesRounds || l == GamesNight
}

// Awards reports whether the night ends with awards: every level but Off.
func (g Games) Awards() bool { return g.LevelOf() != GamesOff }

func (g Games) validate() error {
	if g.Level != "" {
		if _, ok := gameLevels[g.Level]; !ok {
			return &InvalidInputError{"the games level is off, recap, ambient, rounds or gamenight"}
		}
	}
	for k := range g.Enabled {
		if !slices.Contains(GameKinds, k) {
			return &InvalidInputError{fmt.Sprintf("there's no game called %q", k)}
		}
	}
	if f := g.Frequency; f != nil && (*f < 0 || *f > MaxGameFrequency) {
		return &InvalidInputError{fmt.Sprintf("a round every 0 (only when started) to %d songs", MaxGameFrequency)}
	}
	switch g.Scores {
	case "", ScoresOff, ScoresPrivate, ScoresBoard:
	default:
		return &InvalidInputError{"scores are off, private or board"}
	}
	if b := g.BreaksPerHour; b != nil && (*b < 0 || *b > MaxBreaksPerHour) {
		return &InvalidInputError{fmt.Sprintf("breaks per hour are 0 to %d", MaxBreaksPerHour)}
	}
	if t := g.Tune; t != nil {
		if t.From != "" && !slices.Contains(TuneSources, t.From) {
			return &InvalidInputError{"name that tune's songs are from tonight, favorites or new"}
		}
		if t.Clip != "" && !slices.Contains(ClipSpots, t.Clip) {
			return &InvalidInputError{"name that tune's clips are from the chorus, intro or outro"}
		}
	}
	return nil
}

// clean drops switches that say what the level would anyway, so a later
// level change brings its own defaults.
func (g *Games) clean() {
	if g.Level == GamesOff {
		g.Level = ""
	}
	maps.DeleteFunc(g.Enabled, func(k string, on bool) bool {
		return on == slices.Contains(gameLevels[g.LevelOf()].on, k)
	})
	if len(g.Enabled) == 0 {
		g.Enabled = nil
	}
	if t := g.Tune; t != nil {
		if t.From == TuneTonight {
			t.From = ""
		}
		if t.Clip == ClipChorus {
			t.Clip = ""
		}
		if *t == (Tune{}) {
			g.Tune = nil
		}
	}
}
