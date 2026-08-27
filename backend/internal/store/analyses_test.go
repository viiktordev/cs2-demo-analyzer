package store_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/demo"
	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/store"
)

func record(id, steamID string, at time.Time) *store.Record {
	return &store.Record{
		ID:       id,
		SteamID:  steamID,
		FileName: id + ".dem",
		Analysis: &demo.Analysis{
			Summary: demo.Summary{Map: "de_mirage", Rounds: 22},
			Player:  demo.PlayerStats{SteamID: steamID, Name: "player", Rating: 1.2},
		},
		AnalyzedAt: at,
	}
}

// TestAnalysesRoundTrip is the guarantee cross-match comparison rests on: an
// analysis has to survive the process that produced it.
func TestAnalysesRoundTrip(t *testing.T) {
	dir := t.TempDir()

	a, err := store.NewAnalyses(dir)
	if err != nil {
		t.Fatal(err)
	}

	const steamID = "76561198410645260"

	if err := a.Save(record("abc", steamID, time.Now().UTC())); err != nil {
		t.Fatal(err)
	}

	// A second store over the same directory stands in for a restart.
	reopened, err := store.NewAnalyses(dir)
	if err != nil {
		t.Fatal(err)
	}

	got, err := reopened.Get(steamID, "abc")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}

	if got.Analysis.Map != "de_mirage" || got.Analysis.Player.Rating != 1.2 {
		t.Errorf("record came back changed: %+v", got.Analysis)
	}

	found, err := reopened.Find("abc")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}

	if found.SteamID != steamID {
		t.Errorf("Find returned steamId %q, want %q", found.SteamID, steamID)
	}
}

func TestAnalysesByPlayerIsNewestFirst(t *testing.T) {
	a, err := store.NewAnalyses(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	const steamID = "76561198410645260"

	now := time.Now().UTC()

	if err := a.Save(record("older", steamID, now.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}

	if err := a.Save(record("newer", steamID, now)); err != nil {
		t.Fatal(err)
	}

	// Another player's records must not leak in.
	if err := a.Save(record("other", "76561198000000001", now)); err != nil {
		t.Fatal(err)
	}

	got, err := a.ByPlayer(steamID)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}

	if got[0].ID != "newer" {
		t.Errorf("first record is %q, want the newest", got[0].ID)
	}
}

func TestAnalysesMissing(t *testing.T) {
	a, err := store.NewAnalyses(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.Get("76561198410645260", "nope"); !errors.Is(err, store.ErrRecordNotFound) {
		t.Errorf("Get: got %v, want ErrRecordNotFound", err)
	}

	if _, err := a.Find("nope"); !errors.Is(err, store.ErrRecordNotFound) {
		t.Errorf("Find: got %v, want ErrRecordNotFound", err)
	}

	got, err := a.ByPlayer("76561198410645260")
	if err != nil {
		t.Errorf("ByPlayer on an unknown player: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("got %d records for an unknown player", len(got))
	}
}

// TestAnalysesRejectsPathTraversal guards the filesystem: ids arrive from the
// URL, so a traversal attempt must never become a path.
func TestAnalysesRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()

	a, err := store.NewAnalyses(dir)
	if err != nil {
		t.Fatal(err)
	}

	hostile := []struct{ steamID, id string }{
		{"../../etc", "passwd"},
		{"76561198410645260", "../../../escape"},
		{"..", ".."},
	}

	for _, h := range hostile {
		if err := a.Save(record(h.id, h.steamID, time.Now())); err == nil {
			t.Errorf("Save accepted steamId=%q id=%q", h.steamID, h.id)
		}

		if _, err := a.Get(h.steamID, h.id); !errors.Is(err, store.ErrRecordNotFound) {
			t.Errorf("Get(%q, %q) = %v, want ErrRecordNotFound", h.steamID, h.id, err)
		}
	}

	// Nothing should have escaped the directory.
	escaped, _ := filepath.Glob(filepath.Join(dir, "..", "escape*"))
	if len(escaped) != 0 {
		t.Errorf("files written outside the data directory: %v", escaped)
	}
}

// TestCoachingReportSurvives checks the AI report round-trips as raw JSON, so a
// generated report is never regenerated (and re-billed) on a restart.
func TestCoachingReportSurvives(t *testing.T) {
	dir := t.TempDir()

	a, err := store.NewAnalyses(dir)
	if err != nil {
		t.Fatal(err)
	}

	r := record("abc", "76561198410645260", time.Now().UTC())
	r.Coaching = &store.Coaching{
		Report:       json.RawMessage(`{"summary":"boa partida","drills":[]}`),
		Model:        "claude-opus-5",
		InputTokens:  4800,
		OutputTokens: 1200,
		GeneratedAt:  time.Now().UTC(),
	}

	if err := a.Save(r); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.NewAnalyses(dir)
	if err != nil {
		t.Fatal(err)
	}

	got, err := reopened.Get(r.SteamID, r.ID)
	if err != nil {
		t.Fatal(err)
	}

	if got.Coaching == nil {
		t.Fatal("coaching report was lost")
	}

	var report struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(got.Coaching.Report, &report); err != nil {
		t.Fatalf("stored report is not valid JSON: %v", err)
	}

	if report.Summary != "boa partida" {
		t.Errorf("summary = %q, want it unchanged", report.Summary)
	}

	if got.Coaching.InputTokens != 4800 {
		t.Errorf("token usage was not preserved: %+v", got.Coaching)
	}
}
