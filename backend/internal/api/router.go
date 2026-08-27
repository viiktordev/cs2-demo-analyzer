// Package api wires the HTTP routes for the pro-coach-cs2 backend.
package api

import (
	"log/slog"
	"net/http"

	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/coach"
	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/store"
)

// Config holds everything NewRouter needs to build the handler tree.
type Config struct {
	// FrontendDir is the directory served at /. Leave empty to serve the API only.
	FrontendDir string
	// Store holds uploads through the choose-a-player flow. Required.
	Store *store.Memory
	// Analyses persists finished analyses so a player can be compared across
	// matches. Required.
	Analyses *store.Analyses
	// Coach generates the AI reports. Nil disables only the coaching endpoint;
	// everything else keeps working.
	Coach *coach.Coach
	// UploadDir is where uploaded demos wait between the roster scan and the
	// analysis that consumes them.
	UploadDir string
	Logger    *slog.Logger
	Version   string
}

// NewRouter builds the application handler: the JSON API under /api and the
// static frontend at the root.
func NewRouter(cfg Config) http.Handler {
	h := &handlers{
		store:     cfg.Store,
		analyses:  cfg.Analyses,
		coach:     cfg.Coach,
		uploadDir: cfg.UploadDir,
		logger:    cfg.Logger,
		version:   cfg.Version,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", h.health)
	mux.HandleFunc("GET /api/demos", h.listDemos)
	mux.HandleFunc("POST /api/demos", h.uploadDemo)
	mux.HandleFunc("GET /api/demos/{id}", h.getDemo)
	// Analysing consumes the upload, so it is a POST, not a GET.
	mux.HandleFunc("POST /api/demos/{id}/players/{steamId}/analyze", h.analyzePlayer)
	// Coaching is a POST: it calls out to the model and stores the result.
	mux.HandleFunc("POST /api/analyses/{id}/coaching", h.generateCoaching)
	mux.HandleFunc("GET /api/players/{steamId}", h.playerHistory)

	// Anything under /api that didn't match a route above is a JSON 404, so the
	// frontend never receives HTML where it expects JSON.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "unknown endpoint")
	})

	// Registered without a method: a method-qualified "GET /" would be rejected
	// by ServeMux as ambiguous against the method-less "/api/" pattern above.
	if cfg.FrontendDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(cfg.FrontendDir)))
	}

	return withRecover(cfg.Logger, withLogging(cfg.Logger, mux))
}
