package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/coach"
	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/demo"
	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/store"
)

// matchSummary is one past match in a player's history, without the round
// detail that would make the list heavy.
type matchSummary struct {
	ID          string    `json:"id"`
	FileName    string    `json:"fileName"`
	Map         string    `json:"map"`
	Rounds      int       `json:"rounds"`
	Kills       int       `json:"kills"`
	Deaths      int       `json:"deaths"`
	ADR         float64   `json:"adr"`
	KAST        float64   `json:"kast"`
	Rating      float64   `json:"rating"`
	HasCoaching bool      `json:"hasCoaching"`
	AnalyzedAt  time.Time `json:"analyzedAt"`
}

// generateCoaching runs the model over a stored analysis and keeps the report.
func (h *handlers) generateCoaching(w http.ResponseWriter, r *http.Request) {
	if h.coach == nil {
		writeError(w, http.StatusServiceUnavailable,
			"AI analysis is not configured: set ANTHROPIC_API_KEY or pass -anthropic-key")

		return
	}

	id := r.PathValue("id")

	record, err := h.analyses.Find(id)
	if err != nil {
		if errors.Is(err, store.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "analysis not found")

			return
		}

		h.logger.Error("loading analysis", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "could not load the analysis")

		return
	}

	// The report is deterministic enough and costs money, so it is generated
	// once and reused.
	if record.Coaching != nil {
		writeJSON(w, http.StatusOK, record.Coaching)

		return
	}

	previous, err := h.previousAnalyses(record)
	if err != nil {
		h.logger.Warn("loading player history", "steamId", record.SteamID, "err", err)
	}

	result, err := h.coach.Analyze(r.Context(), record.Analysis, previous)
	if err != nil {
		if errors.Is(err, coach.ErrNotConfigured) {
			// Not the caller's fault, so it has to reach whoever runs the server.
			h.logger.Error("coaching is unusable", "id", id, "err", err)
			writeError(w, http.StatusServiceUnavailable, err.Error())

			return
		}

		h.logger.Error("generating coaching", "id", id, "err", err)
		writeError(w, http.StatusBadGateway, "the AI analysis failed: "+err.Error())

		return
	}

	record.Coaching = &store.Coaching{
		Report:       result.Raw,
		Provider:     result.Provider,
		Model:        result.Model,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		CachedTokens: result.CachedTokens,
		GeneratedAt:  time.Now().UTC(),
	}

	if err := h.analyses.Save(record); err != nil {
		// The report is still worth returning even if it couldn't be filed.
		h.logger.Error("persisting coaching", "id", id, "err", err)
	}

	h.logger.Info("coaching generated",
		"id", id,
		"player", record.Analysis.Player.Name,
		"provider", result.Provider,
		"model", result.Model,
		"inputTokens", result.InputTokens,
		"outputTokens", result.OutputTokens,
		"cachedTokens", result.CachedTokens,
	)

	writeJSON(w, http.StatusOK, record.Coaching)
}

// playerHistory lists every analysed match for a player, newest first.
func (h *handlers) playerHistory(w http.ResponseWriter, r *http.Request) {
	steamID := r.PathValue("steamId")

	records, err := h.analyses.ByPlayer(steamID)
	if err != nil {
		h.logger.Error("loading player history", "steamId", steamID, "err", err)
		writeError(w, http.StatusInternalServerError, "could not load the history")

		return
	}

	matches := make([]matchSummary, 0, len(records))

	var name string

	for _, rec := range records {
		if rec.Analysis == nil {
			continue
		}

		p := rec.Analysis.Player
		name = p.Name

		matches = append(matches, matchSummary{
			ID:          rec.ID,
			FileName:    rec.FileName,
			Map:         rec.Analysis.Map,
			Rounds:      rec.Analysis.Rounds,
			Kills:       p.Kills,
			Deaths:      p.Deaths,
			ADR:         p.ADR,
			KAST:        p.KAST,
			Rating:      p.Rating,
			HasCoaching: rec.Coaching != nil,
			AnalyzedAt:  rec.AnalyzedAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"steamId": steamID,
		"name":    name,
		"matches": matches,
	})
}

// previousAnalyses returns the player's earlier matches, newest first, skipping
// the one being coached.
func (h *handlers) previousAnalyses(current *store.Record) ([]*demo.Analysis, error) {
	records, err := h.analyses.ByPlayer(current.SteamID)
	if err != nil {
		return nil, err
	}

	out := make([]*demo.Analysis, 0, len(records))

	for _, rec := range records {
		if rec.ID == current.ID || rec.Analysis == nil {
			continue
		}

		out = append(out, rec.Analysis)
	}

	return out, nil
}
