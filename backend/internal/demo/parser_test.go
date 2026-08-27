package demo_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/demo"
)

// TestRejectsNonDemo makes sure garbage input fails fast in both passes instead
// of panicking or hanging.
func TestRejectsNonDemo(t *testing.T) {
	junk := []byte("this is not a demo at all")

	if _, err := demo.ScanRoster(bytes.NewReader(junk)); !errors.Is(err, demo.ErrInvalidDemo) {
		t.Errorf("ScanRoster: got %v, want ErrInvalidDemo", err)
	}

	_, err := demo.Analyze(bytes.NewReader(junk), "junk.dem", "76561198000000000")
	if !errors.Is(err, demo.ErrInvalidDemo) {
		t.Errorf("Analyze: got %v, want ErrInvalidDemo", err)
	}
}

// TestAnalyzeRejectsMalformedSteamID checks that a non-numeric ID is refused
// before the demo is read at all.
func TestAnalyzeRejectsMalformedSteamID(t *testing.T) {
	_, err := demo.Analyze(bytes.NewReader(nil), "x.dem", "not-a-steam-id")
	if !errors.Is(err, demo.ErrUnknownPlayer) {
		t.Fatalf("got %v, want ErrUnknownPlayer", err)
	}
}

// demoFixtures returns any demo dropped into testdata/, compressed or not.
func demoFixtures(t *testing.T) []string {
	t.Helper()

	var matches []string

	for _, pattern := range []string{"*.dem", "*.dem.zst", "*.dem.gz", "*.dem.bz2"} {
		found, err := filepath.Glob(filepath.Join("testdata", pattern))
		if err != nil {
			t.Fatal(err)
		}

		matches = append(matches, found...)
	}

	if len(matches) == 0 {
		t.Skip("no demo in testdata/, drop a .dem there to run this test")
	}

	return matches
}

// TestRealDemo walks the two-pass flow against any demo in testdata/: scan the
// roster, then analyze each player it lists.
func TestRealDemo(t *testing.T) {
	for _, path := range demoFixtures(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			roster, err := demo.ScanRoster(f)
			if err != nil {
				t.Fatalf("ScanRoster: %v", err)
			}

			if roster.Map == "" {
				t.Error("map name is empty")
			}

			if len(roster.Players) == 0 {
				t.Fatal("roster is empty")
			}

			t.Logf("map=%s players=%d", roster.Map, len(roster.Players))

			for _, id := range roster.Players {
				if id.SteamID == "" || id.Name == "" {
					t.Errorf("incomplete identity: %+v", id)
				}
			}

			// Analyzing needs its own read of the file: the scan consumed part
			// of the reader.
			for _, id := range roster.Players {
				if _, err := f.Seek(0, 0); err != nil {
					t.Fatal(err)
				}

				a, err := demo.Analyze(f, filepath.Base(path), id.SteamID)
				if err != nil {
					t.Fatalf("Analyze(%s): %v", id.Name, err)
				}

				if a.Player.SteamID != id.SteamID {
					t.Errorf("got stats for %s, want %s", a.Player.SteamID, id.SteamID)
				}

				if len(a.Player.Rounds) != a.Player.RoundsPlayed {
					t.Errorf("%s: %d round entries but roundsPlayed=%d",
						id.Name, len(a.Player.Rounds), a.Player.RoundsPlayed)
				}

				if !a.Truncated && a.Player.RoundsPlayed > a.Rounds {
					t.Errorf("%s: played %d rounds but the match had %d",
						id.Name, a.Player.RoundsPlayed, a.Rounds)
				}

				t.Logf("  %-16s %2d/%2d/%2d adr=%6.1f kast=%5.1f%% rating=%.2f",
					a.Player.Name, a.Player.Kills, a.Player.Deaths, a.Player.Assists,
					a.Player.ADR, a.Player.KAST, a.Player.Rating)
			}
		})
	}
}

// TestAnalyzeUnknownPlayer checks that asking for someone who never played is a
// clean error rather than an empty stat line.
func TestAnalyzeUnknownPlayer(t *testing.T) {
	path := demoFixtures(t)[0]

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	_, err = demo.Analyze(f, filepath.Base(path), "76561198000000000")
	if !errors.Is(err, demo.ErrUnknownPlayer) {
		t.Fatalf("got %v, want ErrUnknownPlayer", err)
	}
}
