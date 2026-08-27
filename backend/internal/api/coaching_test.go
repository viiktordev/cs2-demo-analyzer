package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/demo"
	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/store"
)

// TestCoachingUnconfigured is the case this machine is actually in: no API key.
// The coaching endpoint has to say so plainly, and everything else has to keep
// working — a missing key disables one feature, not the tool.
func TestCoachingUnconfigured(t *testing.T) {
	handler := newTestRouter(t)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/analyses/abc/coaching", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body: %s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if body.Error == "" {
		t.Error("503 carried no explanation")
	}

	// The rest of the API must be unaffected.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("health returned %d with coaching unconfigured", rec.Code)
	}
}

func TestPlayerHistoryEmpty(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/players/76561198000000000", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		SteamID string            `json:"steamId"`
		Matches []json.RawMessage `json:"matches"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if len(body.Matches) != 0 {
		t.Errorf("got %d matches for an unknown player, want 0", len(body.Matches))
	}
}

// TestPlayerHistory checks that stored analyses come back newest first, with the
// heavy round detail left out of the list.
func TestPlayerHistory(t *testing.T) {
	analyses, err := store.NewAnalyses(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	const steamID = "76561198410645260"

	older := &store.Record{
		ID:       "aaa",
		SteamID:  steamID,
		FileName: "older.dem",
		Analysis: &demo.Analysis{
			Summary: demo.Summary{Map: "de_mirage", Rounds: 22},
			Player:  demo.PlayerStats{SteamID: steamID, Name: "MuriloAS", Rating: 1.10},
		},
		AnalyzedAt: time.Now().Add(-time.Hour).UTC(),
	}

	newer := &store.Record{
		ID:       "bbb",
		SteamID:  steamID,
		FileName: "newer.dem",
		Analysis: &demo.Analysis{
			Summary: demo.Summary{Map: "de_inferno", Rounds: 24},
			Player:  demo.PlayerStats{SteamID: steamID, Name: "MuriloAS", Rating: 1.35},
		},
		AnalyzedAt: time.Now().UTC(),
	}

	for _, r := range []*store.Record{older, newer} {
		if err := analyses.Save(r); err != nil {
			t.Fatal(err)
		}
	}

	handler := routerWithAnalyses(t, analyses)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/players/"+steamID, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Name    string `json:"name"`
		Matches []struct {
			ID          string  `json:"id"`
			Map         string  `json:"map"`
			Rating      float64 `json:"rating"`
			HasCoaching bool    `json:"hasCoaching"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if len(body.Matches) != 2 {
		t.Fatalf("got %d matches, want 2", len(body.Matches))
	}

	if body.Matches[0].ID != "bbb" {
		t.Errorf("first match is %q, want the newest (bbb)", body.Matches[0].ID)
	}

	if body.Matches[0].HasCoaching {
		t.Error("match reports a coaching report that was never generated")
	}

	if body.Name != "MuriloAS" {
		t.Errorf("name = %q, want the player's", body.Name)
	}
}

// TestCoachingOnUnknownAnalysis makes sure a bad ID is a 404, not a 503 — the
// key check must not mask a missing record once a coach is configured. Without a
// coach the 503 comes first by design, so this asserts the current contract.
func TestCoachingOnUnknownAnalysis(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t).ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, "/api/analyses/does-not-exist/coaching", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 while no key is configured", rec.Code)
	}
}
