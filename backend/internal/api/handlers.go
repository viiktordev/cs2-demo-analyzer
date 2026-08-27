package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/coach"
	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/demo"
	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/store"
)

const (
	// maxDemoSize caps an uploaded demo. Pro MR12 demos sit well under this.
	maxDemoSize = 500 << 20 // 500 MiB
	// multipartMemory is how much of the upload is buffered in RAM before
	// spilling to a temp file.
	multipartMemory = 32 << 20 // 32 MiB
	// uploadField is the multipart form field holding the .dem file.
	uploadField = "demo"
)

// UploadExt marks the files this server owns inside the upload directory, so
// cleanup never touches anything else that lives there.
const UploadExt = ".upload"

type handlers struct {
	store     *store.Memory
	analyses  *store.Analyses
	coach     *coach.Coach
	uploadDir string
	logger    *slog.Logger
	version   string
}

// demoView is the API shape of a demo: the roster is available right after the
// upload, the analysis only once a player has been chosen.
type demoView struct {
	ID         string                `json:"id"`
	FileName   string                `json:"fileName"`
	Status     store.State           `json:"status"`
	Map        string                `json:"map"`
	Players    []demo.PlayerIdentity `json:"players,omitempty"`
	Analysis   *demo.Analysis        `json:"analysis,omitempty"`
	UploadedAt time.Time             `json:"uploadedAt"`
}

func viewOf(e *store.Entry) demoView {
	v := demoView{
		ID:         e.ID,
		FileName:   e.FileName,
		Status:     e.State,
		Analysis:   e.Analysis,
		UploadedAt: e.UploadedAt,
	}

	if e.Roster != nil {
		v.Map = e.Roster.Map
		v.Players = e.Roster.Players
	}

	return v
}

// health reports that the API is up.
func (h *handlers) health(w http.ResponseWriter, _ *http.Request) {
	body := map[string]any{
		"status":  "ok",
		"version": h.version,
		// The frontend uses this to decide whether to offer the AI analysis.
		"coaching": h.coach != nil,
	}

	if h.coach != nil {
		body["provider"] = h.coach.Provider()
		body["model"] = h.coach.Model()
	}

	writeJSON(w, http.StatusOK, body)
}

// listDemos returns every upload, newest first, without the analysis detail.
func (h *handlers) listDemos(w http.ResponseWriter, _ *http.Request) {
	entries := h.store.List()

	demos := make([]demoView, len(entries))
	for i, e := range entries {
		v := viewOf(e)
		v.Players = nil
		v.Analysis = nil
		demos[i] = v
	}

	writeJSON(w, http.StatusOK, map[string]any{"demos": demos})
}

// getDemo returns one upload: its roster, and the analysis if it already ran.
func (h *handlers) getDemo(w http.ResponseWriter, r *http.Request) {
	e, ok := h.store.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "demo not found")

		return
	}

	writeJSON(w, http.StatusOK, viewOf(e))
}

// uploadDemo accepts a multipart .dem, stores it and scans its roster. The
// expensive parse is deferred until a player is chosen.
func (h *handlers) uploadDemo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxDemoSize)

	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "demo exceeds the 500 MiB limit")

			return
		}

		writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())

		return
	}
	defer func() {
		if err := r.MultipartForm.RemoveAll(); err != nil {
			h.logger.Warn("cleaning up upload", "err", err)
		}
	}()

	file, header, err := r.FormFile(uploadField)
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing form field "+uploadField)

		return
	}
	defer file.Close()

	name := filepath.Base(header.Filename)
	if !isDemoFile(name) {
		writeError(w, http.StatusUnsupportedMediaType,
			"expected a .dem file (optionally .zst, .gz or .bz2 compressed)")

		return
	}

	id := newID()

	path, err := h.saveUpload(id, file)
	if err != nil {
		h.logger.Error("storing upload", "file", name, "err", err)
		writeError(w, http.StatusInternalServerError, "could not store the demo")

		return
	}

	roster, err := h.scanRoster(path)
	if err != nil {
		h.removeFile(path)

		if errors.Is(err, demo.ErrInvalidDemo) {
			writeError(w, http.StatusBadRequest, "not a valid CS2 demo")

			return
		}

		h.logger.Error("scanning roster", "file", name, "err", err)
		writeError(w, http.StatusInternalServerError, "could not read the demo")

		return
	}

	entry := h.store.Put(id, name, path, roster)

	h.logger.Info("demo uploaded",
		"id", id,
		"file", name,
		"map", roster.Map,
		"players", len(roster.Players),
	)

	writeJSON(w, http.StatusCreated, viewOf(entry))
}

// analyzePlayer runs the full parse for one player. The upload is consumed: the
// file is deleted afterwards, so a second player means a second upload.
func (h *handlers) analyzePlayer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	path, err := h.store.Claim(id)

	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "demo not found")

		return
	case errors.Is(err, store.ErrNotPending):
		writeError(w, http.StatusGone,
			"this demo has already been analyzed — upload it again to pick another player")

		return
	case err != nil:
		h.logger.Error("claiming demo", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")

		return
	}

	entry, _ := h.store.Get(id)
	steamID := r.PathValue("steamId")

	analysis, err := h.analyze(path, entry.FileName, steamID)
	if err != nil {
		// The parse failed, so the file is still worth keeping for a retry.
		h.store.Release(id)

		switch {
		case errors.Is(err, demo.ErrUnknownPlayer):
			writeError(w, http.StatusNotFound, "player not found in this demo")
		case errors.Is(err, demo.ErrInvalidDemo):
			writeError(w, http.StatusBadRequest, "not a valid CS2 demo")
		default:
			h.logger.Error("analyzing demo", "id", id, "steamId", steamID, "err", err)
			writeError(w, http.StatusInternalServerError, "could not analyze the demo")
		}

		return
	}

	h.store.Complete(id, analysis)
	h.removeFile(path)

	record := &store.Record{
		ID:         id,
		SteamID:    analysis.Player.SteamID,
		FileName:   entry.FileName,
		Analysis:   analysis,
		AnalyzedAt: time.Now().UTC(),
	}

	// A failure to persist shouldn't lose the analysis the user just waited for;
	// it only costs the history.
	if err := h.analyses.Save(record); err != nil {
		h.logger.Error("persisting analysis", "id", id, "err", err)
	}

	h.logger.Info("demo analyzed",
		"id", id,
		"player", analysis.Player.Name,
		"map", analysis.Map,
		"rounds", analysis.Rounds,
	)

	updated, _ := h.store.Get(id)
	writeJSON(w, http.StatusOK, viewOf(updated))
}

// saveUpload streams the upload to the upload directory and returns its path.
func (h *handlers) saveUpload(id string, src io.Reader) (string, error) {
	if err := os.MkdirAll(h.uploadDir, 0o750); err != nil {
		return "", err
	}

	path := filepath.Join(h.uploadDir, id+UploadExt)

	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}

	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		h.removeFile(path)

		return "", err
	}

	if err := dst.Close(); err != nil {
		h.removeFile(path)

		return "", err
	}

	return path, nil
}

func (h *handlers) scanRoster(path string) (*demo.Roster, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return demo.ScanRoster(f)
}

func (h *handlers) analyze(path, fileName, steamID string) (*demo.Analysis, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return demo.Analyze(f, fileName, steamID)
}

func (h *handlers) removeFile(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		h.logger.Warn("removing upload", "path", path, "err", err)
	}
}

// compressionExts are the container extensions demo providers wrap .dem in:
// FACEIT ships .dem.zst, Valve match downloads .dem.bz2.
var compressionExts = []string{".zst", ".gz", ".bz2"}

// isDemoFile reports whether name looks like a demo, with or without a
// compression suffix. The actual format is sniffed from the bytes at parse time.
func isDemoFile(name string) bool {
	name = strings.ToLower(name)

	for _, ext := range compressionExts {
		name = strings.TrimSuffix(name, ext)
	}

	return filepath.Ext(name) == ".dem"
}

// newID returns a random 128-bit hex identifier.
func newID() string {
	var b [16]byte
	// crypto/rand.Read never returns an error as of Go 1.24.
	_, _ = rand.Read(b[:])

	return hex.EncodeToString(b[:])
}
