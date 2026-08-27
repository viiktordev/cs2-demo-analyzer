package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/config"
)

func write(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestLoadDotEnv(t *testing.T) {
	path := write(t, `
# a comment
ANTHROPIC_API_KEY=sk-ant-plain

export ADDR=:9000
QUOTED="quoted value"
SINGLE='literal $notexpanded'
EMPTY=
WITH_EQUALS=a=b=c
ESCAPED="line\nbreak"
`)

	want := map[string]string{
		"ANTHROPIC_API_KEY": "sk-ant-plain",
		"ADDR":              ":9000",
		"QUOTED":            "quoted value",
		"SINGLE":            "literal $notexpanded",
		"EMPTY":             "",
		"WITH_EQUALS":       "a=b=c",
		"ESCAPED":           "line\nbreak",
	}

	// t.Setenv registers the cleanup that restores the real environment; the
	// unset is what lets the file's value through.
	for key := range want {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}

	loaded, err := config.LoadDotEnv(path)
	if err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}

	if loaded != path {
		t.Errorf("loaded %q, want %q", loaded, path)
	}

	for key, expected := range want {
		if got := os.Getenv(key); got != expected {
			t.Errorf("%s = %q, want %q", key, got, expected)
		}
	}
}

// TestRealEnvironmentWins is the guarantee behind
// `ANTHROPIC_API_KEY=... make run`: an explicit variable must beat the file.
func TestRealEnvironmentWins(t *testing.T) {
	path := write(t, "ANTHROPIC_API_KEY=from-file\n")

	t.Setenv("ANTHROPIC_API_KEY", "from-shell")

	if _, err := config.LoadDotEnv(path); err != nil {
		t.Fatal(err)
	}

	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "from-shell" {
		t.Errorf("got %q, want the shell value to win", got)
	}
}

// TestMissingFileIsNotAnError matters because most runs have no .env at all.
func TestMissingFileIsNotAnError(t *testing.T) {
	loaded, err := config.LoadDotEnv(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("a missing file was an error: %v", err)
	}

	if loaded != "" {
		t.Errorf("loaded %q, want empty", loaded)
	}
}

// TestFirstExistingWins covers the working-directory-then-parent lookup.
func TestFirstExistingWins(t *testing.T) {
	t.Setenv("PICKED", "")
	os.Unsetenv("PICKED")

	second := write(t, "PICKED=second\n")
	first := write(t, "PICKED=first\n")

	loaded, err := config.LoadDotEnv(filepath.Join(t.TempDir(), "absent"), first, second)
	if err != nil {
		t.Fatal(err)
	}

	if loaded != first {
		t.Errorf("loaded %q, want the first existing file", loaded)
	}

	if got := os.Getenv("PICKED"); got != "first" {
		t.Errorf("PICKED = %q, want %q", got, "first")
	}
}

// TestMalformedLineIsReported: a typo in a secrets file should be loud, not
// silently skipped.
func TestMalformedLineIsReported(t *testing.T) {
	path := write(t, "GOOD=1\nthis line has no equals sign\n")

	if _, err := config.LoadDotEnv(path); err == nil {
		t.Fatal("a malformed line was accepted")
	}
}

// TestHashInValueSurvives: API keys are pasted unquoted, and stripping an inline
// comment would silently truncate one containing a #.
func TestHashInValueSurvives(t *testing.T) {
	t.Setenv("SECRET", "")
	os.Unsetenv("SECRET")

	path := write(t, "SECRET=abc#def\n")

	if _, err := config.LoadDotEnv(path); err != nil {
		t.Fatal(err)
	}

	if got := os.Getenv("SECRET"); got != "abc#def" {
		t.Errorf("SECRET = %q, want it untouched", got)
	}
}
