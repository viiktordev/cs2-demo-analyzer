// Package demo parses CS2 demo files (.dem) into analysis summaries.
package demo

import "time"

// TeamScore holds the final state of one of the two teams.
type TeamScore struct {
	// Side is "CT" or "T" — the side the team ended the match on.
	Side string `json:"side"`
	Name string `json:"name"`
	// Score is the number of rounds won.
	Score int `json:"score"`
}

// RoundInfo is a single round of the match.
type RoundInfo struct {
	Number int `json:"number"`
	// Winner is "CT", "T" or "" for a draw.
	Winner string `json:"winner"`
	// Reason is the human-readable round end reason, e.g. "bomb defused".
	Reason string `json:"reason"`
	// ElapsedSeconds is the demo time at which the round ended.
	ElapsedSeconds float64 `json:"elapsedSeconds"`
}

// PlayerRound is one player's contribution to a single round.
type PlayerRound struct {
	Round   int `json:"round"`
	Kills   int `json:"kills"`
	Assists int `json:"assists"`
	Deaths  int `json:"deaths"`
	Damage  int `json:"damage"`
	// Survived is true when the player was still alive at the round end.
	Survived bool `json:"survived"`
	// Traded is true when a teammate killed this player's killer shortly after.
	Traded bool `json:"traded"`
	// KAST is true when the round counted towards the player's KAST: a kill,
	// an assist, survival or being traded.
	KAST bool `json:"kast"`

	// Side is "CT" or "T" — sides swap at half, so it is per round.
	Side string `json:"side,omitempty"`
	// Won is true when the player's side took the round.
	Won bool `json:"won"`

	// BuyType is "eco", "force" or "full", from what the player was holding
	// when the freeze time ended.
	BuyType string `json:"buyType,omitempty"`
	// MoneyLeftover is the cash still in hand once the buy period closed — money
	// that could have become equipment. Measured at the freeze-time end, not at
	// the round end, where it would already include the round payout.
	MoneyLeftover int `json:"moneyLeftover"`

	ShotsFired int `json:"shotsFired"`
	ShotsHit   int `json:"shotsHit"`

	// UtilityThrown counts grenades by kind, e.g. {"flash": 2, "smoke": 1}.
	UtilityThrown map[string]int `json:"utilityThrown,omitempty"`
	// UtilityUnused is what was still in the inventory at death.
	UtilityUnused int `json:"utilityUnused"`
	// TeammatesFlashed counts own teammates blinded by this player.
	TeammatesFlashed int `json:"teammatesFlashed"`

	// Death describes how the round ended for the player, nil if they survived.
	Death *DeathContext `json:"death,omitempty"`
}

// DeathContext is the situation around a player's death — the part that turns a
// number into something coachable.
type DeathContext struct {
	// Seconds is the demo time of the death, measured from the round start.
	Seconds float64 `json:"seconds"`
	// X and Y are world coordinates. They are only meaningful relative to other
	// deaths on the same map: no radar calibration is applied.
	X float64 `json:"x"`
	Y float64 `json:"y"`
	// Weapon is what killed the player, Distance how far the killer stood.
	Weapon   string  `json:"weapon"`
	Distance float64 `json:"distance"`
	// TeammatesAlive and EnemiesAlive are the counts at the moment of death,
	// which is what says whether the player was trading, stranded or overrun.
	TeammatesAlive int `json:"teammatesAlive"`
	EnemiesAlive   int `json:"enemiesAlive"`
}

// DeathCluster groups deaths that happened close to each other. Dying in the
// same place over and over is one of the clearest coachable patterns, and it
// falls out of the coordinates alone — no map callouts or radar assets needed.
type DeathCluster struct {
	Count int `json:"count"`
	// X and Y are the centroid of the grouped deaths.
	X float64 `json:"x"`
	Y float64 `json:"y"`
	// Rounds lists which rounds ended there.
	Rounds []int `json:"rounds"`
}

// WeaponAccuracy is the hit rate for one class of weapon, e.g. rifles.
type WeaponAccuracy struct {
	Class      string  `json:"class"`
	ShotsFired int     `json:"shotsFired"`
	ShotsHit   int     `json:"shotsHit"`
	Accuracy   float64 `json:"accuracy"`
}

// BuyTypeSplit is how the player performed under one economy state.
type BuyTypeSplit struct {
	BuyType string `json:"buyType"`
	Rounds  int    `json:"rounds"`
	Won     int    `json:"won"`
	Kills   int    `json:"kills"`
	// ADR here is damage per round within this buy type only.
	ADR float64 `json:"adr"`
}

// CoachingFeatures is the derived, compact view of a player's match, shaped for
// an LLM to interpret. It holds only aggregates that need arithmetic across
// rounds — anything the model can read straight off PlayerStats.Rounds is left
// out on purpose, to keep the prompt small.
type CoachingFeatures struct {
	// DeathClusters is ordered by size, biggest first, and only keeps groups
	// with more than one death.
	DeathClusters []DeathCluster `json:"deathClusters,omitempty"`
	// AccuracyByClass is ordered by shots fired, most used first.
	AccuracyByClass []WeaponAccuracy `json:"accuracyByClass,omitempty"`
	// ByBuyType splits the match by economy state.
	ByBuyType []BuyTypeSplit `json:"byBuyType,omitempty"`

	// RoundsWithLeftoverMoney counts rounds that ended with cash the player
	// could have spent, and how much on average.
	RoundsWithLeftoverMoney int     `json:"roundsWithLeftoverMoney"`
	AvgMoneyLeftover        float64 `json:"avgMoneyLeftover"`

	// UtilityPerRound is grenades thrown per round played.
	UtilityPerRound float64 `json:"utilityPerRound"`
	// UtilityWastedRounds counts rounds where the player died still holding
	// grenades.
	UtilityWastedRounds int `json:"utilityWastedRounds"`
	// TeamFlashes counts teammates blinded across the match.
	TeamFlashes int `json:"teamFlashes"`

	// WinRateWhenSurviving and WinRateWhenDying expose whether the player's
	// death is what decides their rounds.
	WinRateWhenSurviving float64 `json:"winRateWhenSurviving"`
	WinRateWhenDying     float64 `json:"winRateWhenDying"`
}

// PlayerStats aggregates one player's performance across the match.
type PlayerStats struct {
	// SteamID is a 64-bit ID rendered as a string: it exceeds the range
	// JavaScript can represent exactly as a number.
	SteamID string `json:"steamId"`
	Name    string `json:"name"`
	// Team is the clan name of the team the player finished on.
	Team string `json:"team"`
	// Side is "CT" or "T" — the side the player finished the match on.
	Side string `json:"side"`

	RoundsPlayed int `json:"roundsPlayed"`
	Kills        int `json:"kills"`
	Deaths       int `json:"deaths"`
	Assists      int `json:"assists"`
	Headshots    int `json:"headshots"`
	// MVPs stays 0 on demos whose server doesn't emit the round_mvp event,
	// which is common on third-party platforms.
	MVPs int `json:"mvps"`

	// Damage counts enemy health damage, capped at what the victim had left.
	Damage        int `json:"damage"`
	UtilityDamage int `json:"utilityDamage"`

	// KD is kills per death, falling back to kills when the player never died.
	KD float64 `json:"kd"`
	// HeadshotPct is the share of kills that were headshots, 0-100.
	HeadshotPct float64 `json:"headshotPct"`
	// ADR is average damage per round.
	ADR float64 `json:"adr"`
	// KAST is the share of rounds with a kill, assist, survival or trade, 0-100.
	KAST float64 `json:"kast"`
	// Rating follows the HLTV 1.0 formula.
	Rating float64 `json:"rating"`

	// OpeningKills and OpeningDeaths count the first duel of each round.
	OpeningKills  int `json:"openingKills"`
	OpeningDeaths int `json:"openingDeaths"`
	// TradeKills counts kills that avenged a teammate within the trade window.
	TradeKills int `json:"tradeKills"`

	// MultiKills indexes rounds by kill count: [0] is 1k, [4] is 5k.
	MultiKills [5]int `json:"multiKills"`

	EnemiesFlashed int `json:"enemiesFlashed"`
	FlashAssists   int `json:"flashAssists"`

	BombsPlanted int `json:"bombsPlanted"`
	BombsDefused int `json:"bombsDefused"`

	// ClutchesPlayed counts rounds entered as the last player alive on their
	// side facing at least one live enemy; ClutchesWon are the ones their team
	// went on to win.
	ClutchesWon    int `json:"clutchesWon"`
	ClutchesPlayed int `json:"clutchesPlayed"`

	// Rounds is the per-round breakdown, omitted from list responses.
	Rounds []PlayerRound `json:"rounds,omitempty"`
	// Coaching holds the cross-round aggregates that feed the AI analysis.
	Coaching *CoachingFeatures `json:"coaching,omitempty"`
}

// PlayerIdentity is just enough to pick a player out of a demo: it comes from
// the roster scan, before any statistics exist.
type PlayerIdentity struct {
	// SteamID is a 64-bit ID rendered as a string: it exceeds the range
	// JavaScript can represent exactly as a number.
	SteamID string `json:"steamId"`
	Name    string `json:"name"`
	// Team is the clan name, Side the "CT"/"T" the player started on.
	Team string `json:"team"`
	Side string `json:"side"`
}

// Roster is the result of the cheap first pass over a demo: the map and who
// played it. Scanning stops at the match start, so it reads a small fraction of
// the file and lets the caller choose a player before the real parse runs.
type Roster struct {
	Map string `json:"map"`
	// Players is ordered by team, then name.
	Players []PlayerIdentity `json:"players"`
}

// Analysis is the full parse: match-level facts plus the statistics of the one
// player that was asked for.
type Analysis struct {
	Summary
	Player PlayerStats `json:"player"`
}

// Summary is the match-level result of parsing a demo.
type Summary struct {
	ID       string `json:"id"`
	FileName string `json:"fileName"`
	Map      string `json:"map"`
	// TickRate is the server tick rate the match ran on.
	TickRate        float64      `json:"tickRate"`
	DurationSeconds float64      `json:"durationSeconds"`
	Rounds          int          `json:"rounds"`
	Score           [2]TeamScore `json:"score"`
	RoundHistory    []RoundInfo  `json:"roundHistory,omitempty"`
	// Truncated is true when the demo stream ended before the match did.
	Truncated bool      `json:"truncated"`
	ParsedAt  time.Time `json:"parsedAt"`
}
