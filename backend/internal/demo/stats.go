package demo

import (
	"fmt"
	"strconv"

	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"
)

// Reference averages behind the HLTV 1.0 rating. They are the league-wide means
// the rating normalises each component against.
const (
	avgKillsPerRound     = 0.679
	avgSurvivalPerRound  = 0.317
	avgMultiKillFactor   = 1.277
	ratingComponentCount = 2.7
)

// toStats converts raw counters into the API model, deriving every rate.
func (a *playerAgg) toStats() PlayerStats {
	s := PlayerStats{
		SteamID:        strconv.FormatUint(a.steamID, 10),
		Name:           a.name,
		Team:           a.team,
		Side:           sideName(a.side),
		RoundsPlayed:   a.roundsPlayed,
		Kills:          a.kills,
		Deaths:         a.deaths,
		Assists:        a.assists,
		Headshots:      a.headshots,
		MVPs:           a.mvps,
		Damage:         a.damage,
		UtilityDamage:  a.utilityDamage,
		OpeningKills:   a.openingKills,
		OpeningDeaths:  a.openingDeaths,
		TradeKills:     a.tradeKills,
		MultiKills:     a.multiKills,
		EnemiesFlashed: a.enemiesFlashed,
		FlashAssists:   a.flashAssists,
		BombsPlanted:   a.bombsPlanted,
		BombsDefused:   a.bombsDefused,
		ClutchesWon:    a.clutchesWon,
		ClutchesPlayed: a.clutchesPlayed,
		Rounds:         a.rounds,
	}

	// A player who never died has an undefined ratio; report their kills.
	if a.deaths > 0 {
		s.KD = round2(float64(a.kills) / float64(a.deaths))
	} else {
		s.KD = float64(a.kills)
	}

	if a.kills > 0 {
		s.HeadshotPct = round2(100 * float64(a.headshots) / float64(a.kills))
	}

	if a.roundsPlayed > 0 {
		rounds := float64(a.roundsPlayed)
		s.ADR = round2(float64(a.damage) / rounds)
		s.KAST = round2(100 * float64(a.kastRounds) / rounds)
		s.Rating = round2(a.rating(rounds))
	}

	return s
}

// rating is the HLTV 1.0 rating: kill output, survival and multi-kill rounds,
// each normalised against the league average and averaged together.
func (a *playerAgg) rating(rounds float64) float64 {
	killRating := (float64(a.kills) / rounds) / avgKillsPerRound
	survivalRating := ((rounds - float64(a.deaths)) / rounds) / avgSurvivalPerRound

	// Multi-kill rounds are weighted by the square of the kill count.
	weighted := 0.0
	for kills, count := range a.multiKills {
		n := float64(kills + 1)
		weighted += n * n * float64(count)
	}

	multiKillRating := (weighted / rounds) / avgMultiKillFactor

	return (killRating + 0.7*survivalRating + multiKillRating) / ratingComponentCount
}

// round2 trims a rate to two decimals so the JSON stays readable.
func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// isPlayingSide reports whether team is one of the two playing sides, as
// opposed to spectators or an unassigned slot.
func isPlayingSide(team common.Team) bool {
	return team == common.TeamTerrorists || team == common.TeamCounterTerrorists
}

// isEnemy reports whether a and b are on opposing playing sides. It is the
// guard against crediting team damage and team kills.
func isEnemy(a, b *common.Player) bool {
	if a == nil || b == nil {
		return false
	}

	return isPlayingSide(a.Team) && isPlayingSide(b.Team) && a.Team != b.Team
}

// opposing returns the other playing side.
func opposing(team common.Team) common.Team {
	if team == common.TeamTerrorists {
		return common.TeamCounterTerrorists
	}

	return common.TeamTerrorists
}

// isUtility reports whether the damage came from a grenade rather than a gun.
func isUtility(weapon *common.Equipment) bool {
	if weapon == nil {
		return false
	}

	switch weapon.Type {
	case common.EqHE, common.EqMolotov, common.EqIncendiary:
		return true
	default:
		return false
	}
}

func teamScore(ts *common.TeamState) TeamScore {
	if ts == nil {
		return TeamScore{}
	}

	return TeamScore{
		Side:  sideName(ts.Team()),
		Name:  ts.ClanName(),
		Score: ts.Score(),
	}
}

func sideName(t common.Team) string {
	switch t {
	case common.TeamCounterTerrorists:
		return "CT"
	case common.TeamTerrorists:
		return "T"
	default:
		return ""
	}
}

func roundEndReason(r events.RoundEndReason) string {
	switch r {
	case events.RoundEndReasonTargetBombed:
		return "bomb exploded"
	case events.RoundEndReasonBombDefused:
		return "bomb defused"
	case events.RoundEndReasonCTWin:
		return "terrorists eliminated"
	case events.RoundEndReasonTerroristsWin:
		return "counter-terrorists eliminated"
	case events.RoundEndReasonTargetSaved:
		return "time ran out"
	case events.RoundEndReasonHostagesRescued:
		return "hostages rescued"
	case events.RoundEndReasonDraw:
		return "draw"
	case events.RoundEndReasonTerroristsSurrender:
		return "terrorists surrendered"
	case events.RoundEndReasonCTSurrender:
		return "counter-terrorists surrendered"
	default:
		return fmt.Sprintf("reason %d", r)
	}
}
