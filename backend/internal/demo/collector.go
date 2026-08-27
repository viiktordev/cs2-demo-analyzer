package demo

import (
	demoinfocs "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"
)

// tradeWindowSeconds is how long after a death a teammate's revenge kill still
// counts as a trade. Five seconds is the window HLTV uses.
const tradeWindowSeconds = 5.0

// killRecord is one kill kept for the round, so trades can be resolved once the
// round is over and the whole sequence is known.
type killRecord struct {
	killerID   uint64
	victimID   uint64
	killerSide common.Team
	victimSide common.Team
	seconds    float64
}

// playerAgg accumulates one player's numbers across the match. It mirrors
// PlayerStats but holds raw counters; the derived rates are computed in stats().
type playerAgg struct {
	steamID uint64
	name    string
	team    string
	side    common.Team

	roundsPlayed  int
	kills         int
	deaths        int
	assists       int
	headshots     int
	mvps          int
	damage        int
	utilityDamage int

	openingKills  int
	openingDeaths int
	tradeKills    int

	// multiKills indexes rounds by kill count: [0] is a 1k, [4] a 5k.
	multiKills [5]int
	kastRounds int

	enemiesFlashed int
	flashAssists   int
	bombsPlanted   int
	bombsDefused   int
	// clutchesPlayed counts rounds the player entered as the last one alive on
	// their side against at least one live enemy — hopeless 1v5s included.
	clutchesWon    int
	clutchesPlayed int

	rounds []PlayerRound
}

// roundState is the per-round scratch space the collector resets on every
// round start.
type roundState struct {
	kills []killRecord
	// target is the measured player's contribution to this round.
	target *PlayerRound
	// hadFirstKill guards the opening-duel bookkeeping.
	hadFirstKill bool
	// clutcher maps a side to the player left alone against live enemies. It
	// tracks everyone, not just the target: the target's clutch is only known
	// once the winning side is.
	clutcher map[common.Team]uint64
	// startedAt is the demo time the round began, so death timestamps can be
	// reported relative to the round rather than the match.
	startedAt float64
	// isPistolRound marks the opening round of each half, where nobody can buy
	// and calling the round an eco would be misleading.
	isPistolRound bool
}

func newRoundState() *roundState {
	return &roundState{clutcher: make(map[common.Team]uint64)}
}

// collector turns the parser's event stream into round history and the target
// player's statistics. Everything before MatchStart (warmup and the knife
// round) is discarded, so the numbers line up with the official scoreboard.
//
// Only the target is accumulated, but the whole event stream still has to be
// watched: trades and clutches are defined by what everybody else did.
type collector struct {
	parser demoinfocs.Parser
	// target is the SteamID of the only player being measured.
	target uint64

	history []RoundInfo
	player  *playerAgg
	round   *roundState

	// shotsByClass and hitsByClass accumulate across the whole match, since
	// accuracy is only meaningful over a decent sample.
	shotsByClass map[common.EquipmentClass]int
	hitsByClass  map[common.EquipmentClass]int

	// nextIsPistolRound carries the pistol-round flag from the half boundary to
	// the RoundStart that follows it.
	nextIsPistolRound bool
}

func newCollector(p demoinfocs.Parser, target uint64) *collector {
	return &collector{
		parser:       p,
		target:       target,
		player:       &playerAgg{steamID: target},
		round:        newRoundState(),
		shotsByClass: make(map[common.EquipmentClass]int),
		hitsByClass:  make(map[common.EquipmentClass]int),
	}
}

// register hooks every event the collector needs onto the parser.
func (c *collector) register() {
	p := c.parser

	// Warmup and the knife round happen before this; drop whatever was
	// collected so the match starts from zero.
	p.RegisterEventHandler(func(events.MatchStart) {
		c.history = nil
		c.player = &playerAgg{steamID: c.target}
		c.round = newRoundState()
	})

	p.RegisterEventHandler(func(events.RoundStart) {
		pistol := c.nextIsPistolRound
		c.nextIsPistolRound = false

		c.round = newRoundState()
		c.round.startedAt = p.CurrentTime().Seconds()
		c.round.isPistolRound = pistol
	})

	// The first round of each half is a pistol round: the match opens with one,
	// and every side switch starts another.
	p.RegisterEventHandler(func(events.MatchStart) { c.nextIsPistolRound = true })
	p.RegisterEventHandler(func(events.TeamSideSwitch) { c.nextIsPistolRound = true })

	p.RegisterEventHandler(c.onKill)
	p.RegisterEventHandler(c.onPlayerHurt)
	p.RegisterEventHandler(c.onPlayerFlashed)
	p.RegisterEventHandler(c.onMVP)
	p.RegisterEventHandler(c.onBombPlanted)
	p.RegisterEventHandler(c.onBombDefused)
	p.RegisterEventHandler(c.onRoundEnd)

	c.registerCoaching()
}

// roundNumber is the number the round currently in progress will get.
func (c *collector) roundNumber() int {
	return len(c.history) + 1
}

// agg returns the accumulator when pl is the player being measured, and nil for
// everyone else. Callers guard every credit with it, which is what keeps the
// collection scoped to a single player.
func (c *collector) agg(pl *common.Player) *playerAgg {
	if pl == nil || pl.SteamID64 != c.target || !isPlayingSide(pl.Team) {
		return nil
	}

	// Names and teams can change mid-match; the most recent one wins.
	c.player.name = pl.Name
	c.player.side = pl.Team

	if pl.TeamState != nil {
		c.player.team = pl.TeamState.ClanName()
	}

	return c.player
}

// playerRound returns the target's scratch entry for the round in progress, or
// nil when pl is somebody else.
func (c *collector) playerRound(pl *common.Player) *PlayerRound {
	if pl == nil || pl.SteamID64 != c.target {
		return nil
	}

	if c.round.target == nil {
		c.round.target = &PlayerRound{Round: c.roundNumber(), Survived: true}
	}

	return c.round.target
}

func (c *collector) onKill(e events.Kill) {
	now := c.parser.CurrentTime().Seconds()

	if killer := c.agg(e.Killer); killer != nil && isEnemy(e.Killer, e.Victim) {
		killer.kills++

		if e.IsHeadshot {
			killer.headshots++
		}

		if pr := c.playerRound(e.Killer); pr != nil {
			pr.Kills++
		}

		if !c.round.hadFirstKill {
			killer.openingKills++
		}
	}

	if victim := c.agg(e.Victim); victim != nil {
		victim.deaths++

		if pr := c.playerRound(e.Victim); pr != nil {
			pr.Deaths++
			pr.Survived = false
			c.recordDeath(e)
		}

		if !c.round.hadFirstKill && isEnemy(e.Killer, e.Victim) {
			victim.openingDeaths++
		}
	}

	if assister := c.agg(e.Assister); assister != nil && isEnemy(e.Assister, e.Victim) {
		assister.assists++

		if e.AssistedFlash {
			assister.flashAssists++
		}

		if pr := c.playerRound(e.Assister); pr != nil {
			pr.Assists++
		}
	}

	if e.Killer != nil && e.Victim != nil {
		c.round.kills = append(c.round.kills, killRecord{
			killerID:   e.Killer.SteamID64,
			victimID:   e.Victim.SteamID64,
			killerSide: e.Killer.Team,
			victimSide: e.Victim.Team,
			seconds:    now,
		})
	}

	// Only a real duel opens the round — a suicide or world death shouldn't
	// consume the opening-kill slot.
	if isEnemy(e.Killer, e.Victim) {
		c.round.hadFirstKill = true
	}

	c.detectClutch(e.Victim)
}

// detectClutch notes the moment a side is reduced to a single live player while
// the enemy still has someone alive. justKilled is excluded because the parser
// may not have flagged them dead yet.
func (c *collector) detectClutch(justKilled *common.Player) {
	alive := map[common.Team]int{}
	last := map[common.Team]*common.Player{}

	for _, pl := range c.parser.GameState().Participants().Playing() {
		if pl == justKilled || !pl.IsAlive() || !isPlayingSide(pl.Team) {
			continue
		}

		alive[pl.Team]++
		last[pl.Team] = pl
	}

	for _, side := range []common.Team{common.TeamTerrorists, common.TeamCounterTerrorists} {
		if alive[side] != 1 || alive[opposing(side)] == 0 {
			continue
		}

		if _, already := c.round.clutcher[side]; already {
			continue
		}

		if last[side] == nil {
			continue
		}

		c.round.clutcher[side] = last[side].SteamID64

		if a := c.agg(last[side]); a != nil {
			a.clutchesPlayed++
		}
	}
}

func (c *collector) onPlayerHurt(e events.PlayerHurt) {
	if !isEnemy(e.Attacker, e.Player) {
		return
	}

	a := c.agg(e.Attacker)
	if a == nil {
		return
	}

	// HealthDamageTaken excludes over-damage, matching how scoreboards count.
	a.damage += e.HealthDamageTaken

	if isUtility(e.Weapon) {
		a.utilityDamage += e.HealthDamageTaken
	}

	if pr := c.playerRound(e.Attacker); pr != nil {
		pr.Damage += e.HealthDamageTaken
	}
}

func (c *collector) onPlayerFlashed(e events.PlayerFlashed) {
	if !isEnemy(e.Attacker, e.Player) {
		return
	}

	if a := c.agg(e.Attacker); a != nil {
		a.enemiesFlashed++
	}
}

func (c *collector) onMVP(e events.RoundMVPAnnouncement) {
	if a := c.agg(e.Player); a != nil {
		a.mvps++
	}
}

func (c *collector) onBombPlanted(e events.BombPlanted) {
	if a := c.agg(e.Player); a != nil {
		a.bombsPlanted++
	}
}

func (c *collector) onBombDefused(e events.BombDefused) {
	if a := c.agg(e.Player); a != nil {
		a.bombsDefused++
	}
}

// onRoundEnd closes the round: it resolves trades, awards clutches and folds the
// round's scratch state into the per-player totals.
func (c *collector) onRoundEnd(e events.RoundEnd) {
	number := c.roundNumber()

	c.history = append(c.history, RoundInfo{
		Number:         number,
		Winner:         sideName(e.Winner),
		Reason:         roundEndReason(e.Reason),
		ElapsedSeconds: c.parser.CurrentTime().Seconds(),
	})

	traded := c.resolveTrades()

	// The target only played the round if they were on a side when it ended.
	for _, pl := range c.parser.GameState().Participants().Playing() {
		a := c.agg(pl)
		if a == nil {
			continue
		}

		a.roundsPlayed++

		pr := c.playerRound(pl)
		if pr == nil {
			continue
		}

		pr.Round = number
		pr.Side = sideName(pl.Team)
		pr.Won = pl.Team == e.Winner
		pr.Traded = traded[a.steamID]
		pr.KAST = pr.Kills > 0 || pr.Assists > 0 || pr.Survived || pr.Traded

		if pr.KAST {
			a.kastRounds++
		}

		if pr.Kills >= 1 && pr.Kills <= len(a.multiKills) {
			a.multiKills[pr.Kills-1]++
		}

		a.rounds = append(a.rounds, *pr)
	}

	if id, ok := c.round.clutcher[e.Winner]; ok && id == c.target {
		c.player.clutchesWon++
	}
}

// resolveTrades walks the round's kills and credits each revenge kill that
// landed inside the trade window, returning the set of players who were traded.
func (c *collector) resolveTrades() map[uint64]bool {
	traded := make(map[uint64]bool)

	for i, death := range c.round.kills {
		for _, revenge := range c.round.kills[i+1:] {
			if revenge.seconds-death.seconds > tradeWindowSeconds {
				break
			}

			// The teammate has to kill the player who did the killing.
			if revenge.victimID != death.killerID || revenge.killerSide != death.victimSide {
				continue
			}

			traded[death.victimID] = true

			if revenge.killerID == c.target {
				c.player.tradeKills++
			}

			break
		}
	}

	return traded
}

// stats renders the target's accumulated counters as the API model, with the
// coaching aggregates folded in.
func (c *collector) stats() PlayerStats {
	s := c.player.toStats()
	s.Coaching = c.coachingFeatures()

	return s
}
