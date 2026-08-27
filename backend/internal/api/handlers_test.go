package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/api"
	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/store"
)

// testServer builds a router over a fresh store and a throwaway upload dir,
// returning both so tests can assert on what landed on disk.
func testServer(t *testing.T) (http.Handler, *store.Memory, string) {
	t.Helper()

	dir := t.TempDir()
	demos := store.NewMemory()

	analyses, err := store.NewAnalyses(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	handler := api.NewRouter(api.Config{
		Store:     demos,
		Analyses:  analyses,
		UploadDir: dir,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version:   "test",
	})

	return handler, demos, dir
}

// routerWithAnalyses builds a router over a specific analyses store, for tests
// that need to seed history.
func routerWithAnalyses(t *testing.T, analyses *store.Analyses) http.Handler {
	t.Helper()

	return api.NewRouter(api.Config{
		Store:     store.NewMemory(),
		Analyses:  analyses,
		UploadDir: t.TempDir(),
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version:   "test",
	})
}

func newTestRouter(t *testing.T) http.Handler {
	t.Helper()

	handler, _, _ := testServer(t)

	return handler
}

// uploadRequest builds a multipart POST to /api/demos.
func uploadRequest(t *testing.T, field, fileName, content string) *http.Request {
	t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	part, err := mw.CreateFormFile(field, fileName)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}

	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/demos", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	return req
}

func TestHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Status   string `json:"status"`
		Coaching bool   `json:"coaching"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if body.Status != "ok" {
		t.Errorf("status = %q, want \"ok\"", body.Status)
	}

	// No key is configured in tests, so the frontend must be told not to offer
	// the AI analysis.
	if body.Coaching {
		t.Error("health advertises coaching, but no coach is configured")
	}
}

func TestListDemosEmpty(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/demos", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Demos []json.RawMessage `json:"demos"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if len(body.Demos) != 0 {
		t.Errorf("got %d demos, want 0", len(body.Demos))
	}
}

func TestGetUnknownDemo(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/demos/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAnalyzeUnknownDemo(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/demos/nope/players/76561198000000000/analyze", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestUnknownAPIEndpointReturnsJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/does-not-exist", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
}

// TestRouterWithFrontend guards against the ServeMux pattern conflict between
// the static catch-all and the /api/ routes, which only panics when a frontend
// directory is configured.
func TestRouterWithFrontend(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hi</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}

	analyses, err := store.NewAnalyses(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	handler := api.NewRouter(api.Config{
		FrontendDir: dir,
		UploadDir:   t.TempDir(),
		Store:       store.NewMemory(),
		Analyses:    analyses,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version:     "test",
	})

	t.Run("serves index", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}

		if body := rec.Body.String(); body != "<h1>hi</h1>" {
			t.Errorf("body = %q, want the index file", body)
		}
	})

	t.Run("api still routes", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}

func TestUploadRejects(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		fileName string
		content  string
		want     int
	}{
		{"wrong extension", "demo", "match.txt", "whatever", http.StatusUnsupportedMediaType},
		{"zst of wrong extension", "demo", "match.txt.zst", "whatever", http.StatusUnsupportedMediaType},
		{"wrong field name", "file", "match.dem", "whatever", http.StatusBadRequest},
		{"not a demo", "demo", "match.dem", "definitely not a demo", http.StatusBadRequest},
		{"compressed name accepted", "demo", "match.dem.zst", "not a demo", http.StatusBadRequest},
		{"uppercase name accepted", "demo", "MATCH.DEM", "not a demo", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, demos, dir := testServer(t)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, uploadRequest(t, tt.field, tt.fileName, tt.content))

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tt.want, rec.Body.String())
			}

			// A rejected upload must not leave the demo behind, nor register it.
			left, err := filepath.Glob(filepath.Join(dir, "*"+api.UploadExt))
			if err != nil {
				t.Fatal(err)
			}

			if len(left) != 0 {
				t.Errorf("rejected upload left %d file(s) on disk", len(left))
			}

			if n := len(demos.List()); n != 0 {
				t.Errorf("rejected upload registered %d demo(s)", n)
			}
		})
	}
}
