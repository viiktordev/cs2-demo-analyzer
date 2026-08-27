package demo_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/demo"
)

// TestCoachingFeatures checks the derived features against a real demo. The
// invariants here are the ones that caught actual bugs: accuracy that never
// registered a hit, a buy type read from a stale value, and leftover money
// measured after the round payout had already landed.
func TestCoachingFeatures(t *testing.T) {
	path := demoFixtures(t)[0]

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	roster, err := demo.ScanRoster(f)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}

	a, err := demo.Analyze(f, filepath.Base(path), roster.Players[0].SteamID)
	if err != nil {
		t.Fatal(err)
	}

	p := a.Player

	c := p.Coaching
	if c == nil {
		t.Fatal("coaching features are nil")
	}

	var (
		shots, hits int
		wins        int
		kills       int
		byType      = map[string]int{}
	)

	for _, r := range p.Rounds {
		if r.ShotsHit > r.ShotsFired {
			t.Errorf("round %d: %d hits from %d shots", r.Round, r.ShotsHit, r.ShotsFired)
		}

		if r.Side != "CT" && r.Side != "T" {
			t.Errorf("round %d: side is %q", r.Round, r.Side)
		}

		if r.Survived != (r.Death == nil) {
			t.Errorf("round %d: survived=%v but death context present=%v",
				r.Round, r.Survived, r.Death != nil)
		}

		shots += r.ShotsFired
		hits += r.ShotsHit
		kills += r.Kills
		byType[r.BuyType]++

		if r.Won {
			wins++
		}
	}

	if hits == 0 && shots > 0 {
		t.Error("no hits recorded despite shots fired — the hit source is probably not firing")
	}

	// Every round must land in exactly one buy bucket.
	if byType[""] != 0 {
		t.Errorf("%d rounds have no buy type", byType[""])
	}

	var splitRounds, splitKills int

	for _, s := range c.ByBuyType {
		splitRounds += s.Rounds
		splitKills += s.Kills
	}

	if splitRounds != len(p.Rounds) {
		t.Errorf("buy splits cover %d rounds, want %d", splitRounds, len(p.Rounds))
	}

	if splitKills != kills {
		t.Errorf("buy splits hold %d kills, want %d", splitKills, kills)
	}

	// A pistol round is not an eco: the opening round of each half has to be
	// classified separately.
	if byType["pistol"] == 0 {
		t.Error("no round classified as a pistol round")
	}

	// Accuracy is reported only for guns, one row per class.
	seen := map[string]bool{}

	for _, w := range c.AccuracyByClass {
		if seen[w.Class] {
			t.Errorf("duplicate accuracy row for class %q", w.Class)
		}

		seen[w.Class] = true

		if w.Class == "other" {
			t.Error("non-gun equipment leaked into the accuracy breakdown")
		}

		if w.ShotsFired == 0 {
			t.Errorf("class %q has an accuracy row with no shots", w.Class)
		}
	}

	// Clusters only exist for repeated deaths, and every round they list must
	// actually be a death.
	deathRounds := map[int]bool{}

	for _, r := range p.Rounds {
		if r.Death != nil {
			deathRounds[r.Round] = true
		}
	}

	for _, cl := range c.DeathClusters {
		if cl.Count < 2 {
			t.Errorf("cluster with %d death(s) should have been dropped", cl.Count)
		}

		if len(cl.Rounds) != cl.Count {
			t.Errorf("cluster claims %d deaths but lists %d rounds", cl.Count, len(cl.Rounds))
		}

		for _, round := range cl.Rounds {
			if !deathRounds[round] {
				t.Errorf("cluster references round %d, where the player did not die", round)
			}
		}
	}

	t.Logf("%s: %d rounds, %d/%d shots, buys=%v, %d death clusters",
		p.Name, len(p.Rounds), hits, shots, byType, len(c.DeathClusters))
}
