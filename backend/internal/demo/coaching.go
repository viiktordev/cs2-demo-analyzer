package demo

import (
	"sort"

	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"
)

// Buy-type thresholds, measured on what the player is holding when the freeze
// time ends. They follow the usual community reading of the economy rather than
// any in-game definition.
const (
	ecoEquipmentValue   = 2000
	forceEquipmentValue = 4000
)

// deathClusterRadius groups deaths into the same spot. CS2 world units are
// roughly an inch, so 300 is about the size of a small room — close enough that
// two deaths inside it are the same mistake.
const deathClusterRadius = 300.0

// registerCoaching hooks the events that feed the coaching features. They are
// kept apart from the scoreboard handlers in collector.go because they answer a
// different question: not how well the player did, but why.
func (c *collector) registerCoaching() {
	p := c.parser

	// The buy is settled when the freeze time ends. Note that
	// EquipmentValueFreezeTimeEnd() is one round behind at this point — it still
	// holds the previous round's value — so the current value is the right one.
	// Money left here is genuine leftover; money at round end would include the
	// round's payout.
	p.RegisterEventHandler(func(events.RoundFreezetimeEnd) {
		for _, pl := range p.GameState().Participants().Playing() {
			if pl.SteamID64 != c.target {
				continue
			}

			pr := c.playerRound(pl)
			if pr == nil {
				continue
			}

			pr.MoneyLeftover = pl.Money()

			if c.round.isPistolRound {
				pr.BuyType = "pistol"
			} else {
				pr.BuyType = buyType(pl.EquipmentValueCurrent())
			}
		}
	})

	p.RegisterEventHandler(func(e events.WeaponFire) {
		if e.Shooter == nil || e.Shooter.SteamID64 != c.target {
			return
		}

		if pr := c.playerRound(e.Shooter); pr != nil {
			pr.ShotsFired++
		}

		if isShooting(e.Weapon) {
			c.shotsByClass[e.Weapon.Class()]++
		}
	})

	// Hits are counted from PlayerHurt rather than BulletDamage: the latter is a
	// CS2-only game event that many servers — FACEIT's included — never emit, so
	// relying on it silently yields zero accuracy.
	//
	// One caveat this inherits: a shotgun blast is a single WeaponFire but can
	// register several PlayerHurt, so heavy-class accuracy runs high.
	p.RegisterEventHandler(func(e events.PlayerHurt) {
		if e.Attacker == nil || e.Attacker.SteamID64 != c.target || !isEnemy(e.Attacker, e.Player) {
			return
		}

		if !isShooting(e.Weapon) {
			return
		}

		if pr := c.playerRound(e.Attacker); pr != nil {
			pr.ShotsHit++
		}

		c.hitsByClass[e.Weapon.Class()]++
	})

	p.RegisterEventHandler(func(e events.GrenadeProjectileThrow) {
		if e.Projectile == nil || e.Projectile.Thrower == nil {
			return
		}

		thrower := e.Projectile.Thrower
		if thrower.SteamID64 != c.target {
			return
		}

		if pr := c.playerRound(thrower); pr != nil {
			if pr.UtilityThrown == nil {
				pr.UtilityThrown = make(map[string]int)
			}

			pr.UtilityThrown[grenadeName(e.Projectile.WeaponInstance)]++
		}
	})

	// A flash that blinds your own side is a mistake worth surfacing.
	p.RegisterEventHandler(func(e events.PlayerFlashed) {
		if e.Attacker == nil || e.Attacker.SteamID64 != c.target {
			return
		}

		if e.Player == nil || e.Player == e.Attacker || e.Player.Team != e.Attacker.Team {
			return
		}

		if pr := c.playerRound(e.Attacker); pr != nil {
			pr.TeammatesFlashed++
		}
	})
}

// recordDeath captures the situation around the target's death. It runs from the
// Kill handler, while the game state still reflects the moment.
func (c *collector) recordDeath(e events.Kill) {
	pr := c.playerRound(e.Victim)
	if pr == nil {
		return
	}

	pos := e.Victim.Position()

	death := &DeathContext{
		Seconds:  c.parser.CurrentTime().Seconds() - c.round.startedAt,
		X:        pos.X,
		Y:        pos.Y,
		Distance: float64(e.Distance),
	}

	if e.Weapon != nil {
		death.Weapon = e.Weapon.String()
	}

	for _, pl := range c.parser.GameState().Participants().Playing() {
		if pl == e.Victim || !pl.IsAlive() || !isPlayingSide(pl.Team) {
			continue
		}

		if pl.Team == e.Victim.Team {
			death.TeammatesAlive++
		} else {
			death.EnemiesAlive++
		}
	}

	// Grenades still on the belt at death are utility that never got used.
	for _, w := range e.Victim.Weapons() {
		if w != nil && w.Class() == common.EqClassGrenade {
			pr.UtilityUnused++
		}
	}

	pr.Death = death
}

// buyType reads an equipment value as an economy state.
func buyType(equipmentValue int) string {
	switch {
	case equipmentValue < ecoEquipmentValue:
		return "eco"
	case equipmentValue < forceEquipmentValue:
		return "force"
	default:
		return "full"
	}
}

// grenadeName is the short label used in the utility breakdown.
func grenadeName(e *common.Equipment) string {
	if e == nil {
		return "unknown"
	}

	switch e.Type {
	case common.EqFlash:
		return "flash"
	case common.EqSmoke:
		return "smoke"
	case common.EqHE:
		return "he"
	case common.EqMolotov, common.EqIncendiary:
		return "fire"
	case common.EqDecoy:
		return "decoy"
	default:
		return "unknown"
	}
}

// isShooting reports whether the equipment is a gun, as opposed to a grenade,
// knife or kit. Only guns belong in an accuracy figure.
func isShooting(e *common.Equipment) bool {
	if e == nil {
		return false
	}

	switch e.Class() {
	case common.EqClassPistols, common.EqClassSMG, common.EqClassHeavy, common.EqClassRifle:
		return true
	default:
		return false
	}
}

// className labels an equipment class for the accuracy breakdown. Only the
// classes isShooting accepts can reach it.
func className(c common.EquipmentClass) string {
	switch c {
	case common.EqClassPistols:
		return "pistol"
	case common.EqClassSMG:
		return "smg"
	case common.EqClassHeavy:
		return "heavy"
	case common.EqClassRifle:
		return "rifle"
	default:
		return "other"
	}
}

// coachingFeatures folds the per-round data into the cross-round aggregates that
// go to the model. It is called once, after the parse is done.
func (c *collector) coachingFeatures() *CoachingFeatures {
	rounds := c.player.rounds
	if len(rounds) == 0 {
		return nil
	}

	f := &CoachingFeatures{
		DeathClusters:   clusterDeaths(rounds),
		AccuracyByClass: accuracyByClass(c.shotsByClass, c.hitsByClass),
		ByBuyType:       splitByBuyType(rounds),
	}

	var (
		leftoverTotal          int
		utilityThrown          int
		survived, survivedWins int
		died, diedWins         int
	)

	for _, r := range rounds {
		if r.MoneyLeftover > 0 {
			f.RoundsWithLeftoverMoney++
			leftoverTotal += r.MoneyLeftover
		}

		for _, n := range r.UtilityThrown {
			utilityThrown += n
		}

		if r.UtilityUnused > 0 {
			f.UtilityWastedRounds++
		}

		f.TeamFlashes += r.TeammatesFlashed

		if r.Survived {
			survived++

			if r.Won {
				survivedWins++
			}

			continue
		}

		died++

		if r.Won {
			diedWins++
		}
	}

	if f.RoundsWithLeftoverMoney > 0 {
		f.AvgMoneyLeftover = round2(float64(leftoverTotal) / float64(f.RoundsWithLeftoverMoney))
	}

	f.UtilityPerRound = round2(float64(utilityThrown) / float64(len(rounds)))

	if survived > 0 {
		f.WinRateWhenSurviving = round2(100 * float64(survivedWins) / float64(survived))
	}

	if died > 0 {
		f.WinRateWhenDying = round2(100 * float64(diedWins) / float64(died))
	}

	return f
}

// clusterDeaths groups nearby deaths with single-link agglomeration: a death
// joins the first cluster it is within range of. With at most ~25 points per
// match the quadratic scan is irrelevant, and the result is what matters — the
// spots a player keeps dying in.
func clusterDeaths(rounds []PlayerRound) []DeathCluster {
	var clusters []DeathCluster

	for _, r := range rounds {
		if r.Death == nil {
			continue
		}

		joined := false

		for i := range clusters {
			if !within(clusters[i].X, clusters[i].Y, r.Death.X, r.Death.Y, deathClusterRadius) {
				continue
			}

			// Fold the point into the running centroid.
			n := float64(clusters[i].Count)
			clusters[i].X = (clusters[i].X*n + r.Death.X) / (n + 1)
			clusters[i].Y = (clusters[i].Y*n + r.Death.Y) / (n + 1)
			clusters[i].Count++
			clusters[i].Rounds = append(clusters[i].Rounds, r.Round)
			joined = true

			break
		}

		if !joined {
			clusters = append(clusters, DeathCluster{
				Count:  1,
				X:      r.Death.X,
				Y:      r.Death.Y,
				Rounds: []int{r.Round},
			})
		}
	}

	// A lone death is not a pattern.
	repeated := clusters[:0]

	for _, c := range clusters {
		if c.Count > 1 {
			c.X = round2(c.X)
			c.Y = round2(c.Y)
			repeated = append(repeated, c)
		}
	}

	sort.Slice(repeated, func(i, j int) bool { return repeated[i].Count > repeated[j].Count })

	return repeated
}

// within reports whether two points are closer than radius, comparing squared
// distances so there is no square root in the loop.
func within(x1, y1, x2, y2, radius float64) bool {
	dx, dy := x1-x2, y1-y2

	return dx*dx+dy*dy <= radius*radius
}

func accuracyByClass(shots, hits map[common.EquipmentClass]int) []WeaponAccuracy {
	out := make([]WeaponAccuracy, 0, len(shots))

	for class, fired := range shots {
		if fired == 0 {
			continue
		}

		w := WeaponAccuracy{
			Class:      className(class),
			ShotsFired: fired,
			ShotsHit:   hits[class],
		}
		w.Accuracy = round2(100 * float64(w.ShotsHit) / float64(fired))

		out = append(out, w)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ShotsFired > out[j].ShotsFired })

	return out
}

func splitByBuyType(rounds []PlayerRound) []BuyTypeSplit {
	order := []string{"pistol", "eco", "force", "full"}
	byType := make(map[string]*BuyTypeSplit, len(order))

	damage := make(map[string]int)

	for _, r := range rounds {
		key := r.BuyType
		if key == "" {
			continue
		}

		s, ok := byType[key]
		if !ok {
			s = &BuyTypeSplit{BuyType: key}
			byType[key] = s
		}

		s.Rounds++
		s.Kills += r.Kills
		damage[key] += r.Damage

		if r.Won {
			s.Won++
		}
	}

	out := make([]BuyTypeSplit, 0, len(byType))

	for _, key := range order {
		s, ok := byType[key]
		if !ok {
			continue
		}

		s.ADR = round2(float64(damage[key]) / float64(s.Rounds))
		out = append(out, *s)
	}

	return out
}
