// Package config loads configuration that shouldn't live on the command line.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// LoadDotEnv reads the first of paths that exists and puts its entries into the
// environment. Missing files are not an error: a .env is a convenience, and the
// server has to start without one.
//
// Variables already set in the real environment win, so
// `ANTHROPIC_API_KEY=... make run` still overrides the file.
//
// The format is the common one: blank lines and lines starting with # are
// ignored, an optional `export ` prefix is allowed, and values may be wrapped in
// single or double quotes. Inline comments are deliberately not stripped — a
// secret containing a # should survive being pasted in unquoted.
//
// It returns the path it loaded, or "" when no file was found.
func LoadDotEnv(paths ...string) (string, error) {
	for _, path := range paths {
		if path == "" {
			continue
		}

		f, err := os.Open(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}

			return "", fmt.Errorf("opening %s: %w", path, err)
		}

		defer f.Close()

		if err := apply(f, path); err != nil {
			return "", err
		}

		return path, nil
	}

	return "", nil
}

func apply(f *os.File, path string) error {
	scanner := bufio.NewScanner(f)

	for line := 1; scanner.Scan(); line++ {
		key, value, err := parseLine(scanner.Text())
		if err != nil {
			return fmt.Errorf("%s:%d: %w", path, line, err)
		}

		if key == "" {
			continue
		}

		// The real environment is the more explicit choice, so it wins.
		if _, set := os.LookupEnv(key); set {
			continue
		}

		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("%s:%d: setting %s: %w", path, line, key, err)
		}
	}

	return scanner.Err()
}

// parseLine returns the key and value on a line, or an empty key for lines that
// hold nothing to set.
func parseLine(raw string) (key, value string, err error) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", nil
	}

	line = strings.TrimPrefix(line, "export ")

	name, rest, found := strings.Cut(line, "=")
	if !found {
		return "", "", fmt.Errorf("expected KEY=VALUE, got %q", raw)
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", fmt.Errorf("missing name before '=' in %q", raw)
	}

	return name, unquote(strings.TrimSpace(rest)), nil
}

// unquote strips a matching pair of surrounding quotes. Inside double quotes the
// usual escapes are expanded; single quotes are literal.
func unquote(v string) string {
	if len(v) < 2 {
		return v
	}

	first, last := v[0], v[len(v)-1]
	if first != last {
		return v
	}

	switch first {
	case '\'':
		return v[1 : len(v)-1]
	case '"':
		inner := v[1 : len(v)-1]
		replacer := strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\"`, `"`, `\\`, `\`)

		return replacer.Replace(inner)
	default:
		return v
	}
}
