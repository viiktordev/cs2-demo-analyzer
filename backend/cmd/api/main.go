// Command api serves the pro-coach-cs2 JSON API and the static frontend.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/api"
	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/coach"
	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/config"
	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/store"
)

// version is overridable at build time with -ldflags "-X main.version=...".
var version = "dev"

const shutdownTimeout = 10 * time.Second

func main() {
	// The .env has to be read before the flags, since their defaults are taken
	// from the environment as it stands at definition time.
	envFile, envErr := config.LoadDotEnv(dotEnvPaths()...)

	addr := flag.String("addr", env("ADDR", ":8080"), "address to listen on")
	frontendDir := flag.String("frontend", env("FRONTEND_DIR", "../frontend"),
		"directory with the static frontend (empty to disable)")
	uploadDir := flag.String("uploads", env("UPLOAD_DIR", defaultUploadDir()),
		"directory holding uploaded demos between the roster scan and the analysis")
	dataDir := flag.String("data", env("DATA_DIR", "./data"),
		"directory where finished analyses are kept, so players can be compared across matches")
	provider := flag.String("provider", env("AI_PROVIDER", coach.ProviderAuto),
		"which model backend to use: anthropic, openai, or auto for whichever key is set")
	anthropicKey := flag.String("anthropic-key", env("ANTHROPIC_API_KEY", ""),
		"Anthropic API key; prefer the environment or a .env over this flag")
	openaiKey := flag.String("openai-key", env("OPENAI_API_KEY", ""),
		"OpenAI API key; prefer the environment or a .env over this flag")
	model := flag.String("model", env("AI_MODEL", ""),
		"override the chosen provider's default model")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Reported after the logger exists, so a broken .env is visible rather than
	// silently ignored.
	if envErr != nil {
		logger.Error("reading the .env file", "err", envErr)
		os.Exit(1)
	}

	if envFile != "" {
		logger.Info("loaded environment file", "path", envFile)
	}

	// The store is in-memory, so any file already in the upload directory is
	// orphaned by definition: nothing left can claim it.
	sweepUploads(*uploadDir, logger)

	demos := store.NewMemory()

	analyses, err := store.NewAnalyses(*dataDir)
	if err != nil {
		logger.Error("opening the data directory", "dir", *dataDir, "err", err)
		os.Exit(1)
	}

	// A missing key disables the coaching endpoint and nothing else, so the tool
	// stays useful without one.
	var trainer *coach.Coach

	if c, err := coach.New(coach.Options{
		Provider:     *provider,
		AnthropicKey: *anthropicKey,
		OpenAIKey:    *openaiKey,
		Model:        *model,
	}); err != nil {
		if !errors.Is(err, coach.ErrNotConfigured) {
			logger.Error("setting up the coach", "err", err)
			os.Exit(1)
		}

		logger.Warn("AI analysis disabled", "reason", err)
	} else {
		trainer = c
		logger.Info("AI analysis enabled", "provider", c.Provider(), "model", c.Model())
	}

	handler := api.NewRouter(api.Config{
		FrontendDir: *frontendDir,
		UploadDir:   *uploadDir,
		Store:       demos,
		Analyses:    analyses,
		Coach:       trainer,
		Logger:      logger,
		Version:     version,
	})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// Demo uploads are large and parsing happens inline, so the read and
		// write budgets are deliberately generous.
		ReadTimeout:  15 * time.Minute,
		WriteTimeout: 15 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("listening",
			"addr", *addr,
			"frontend", *frontendDir,
			"uploads", *uploadDir,
			"data", *dataDir,
			"coaching", trainer != nil,
			"version", version,
		)

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	// Demos that were uploaded but never analyzed would outlive the store.
	for _, path := range demos.Paths() {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Warn("removing upload", "path", path, "err", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
}

// defaultUploadDir keeps uploads out of the working tree: they are scratch
// space, deleted as soon as the analysis that consumes them finishes.
func defaultUploadDir() string {
	return filepath.Join(os.TempDir(), "pro-coach-cs2-uploads")
}

// sweepUploads deletes leftover demo files from a previous run. It only removes
// files this server named, so pointing -uploads at a populated directory can't
// destroy anything else.
func sweepUploads(dir string, logger *slog.Logger) {
	matches, err := filepath.Glob(filepath.Join(dir, "*"+api.UploadExt))
	if err != nil {
		logger.Warn("sweeping uploads", "dir", dir, "err", err)

		return
	}

	for _, path := range matches {
		if err := os.Remove(path); err != nil {
			logger.Warn("removing stale upload", "path", path, "err", err)

			continue
		}

		logger.Info("removed stale upload", "path", path)
	}
}

// dotEnvPaths is where the .env is looked for. The working directory comes
// first, then its parent, so the same repo-root file works whether the server is
// started from the repo or from backend/ the way `make run` does. ENV_FILE
// overrides both.
func dotEnvPaths() []string {
	if custom := os.Getenv("ENV_FILE"); custom != "" {
		return []string{custom}
	}

	return []string{".env", filepath.Join("..", ".env")}
}

// env returns the value of key, or fallback when it is unset or empty.
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}
