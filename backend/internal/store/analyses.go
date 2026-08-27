package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/demo"
)

// Record is one analysed match, as persisted. It outlives the process, which is
// what makes comparing a player across matches possible.
type Record struct {
	ID       string         `json:"id"`
	SteamID  string         `json:"steamId"`
	FileName string         `json:"fileName"`
	Analysis *demo.Analysis `json:"analysis"`
	// Coaching is the AI report, nil until one has been generated.
	Coaching   *Coaching `json:"coaching,omitempty"`
	AnalyzedAt time.Time `json:"analyzedAt"`
}

// Coaching is the model's report on a match, stored alongside it so it is
// generated once rather than on every view.
type Coaching struct {
	Report json.RawMessage `json:"report"`
	// Provider and Model record which backend produced this report, since the
	// server can be pointed at either one.
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	// CachedTokens is how much of the prompt was served from the cache.
	CachedTokens int64     `json:"cachedTokens"`
	GeneratedAt  time.Time `json:"generatedAt"`
}

// ErrRecordNotFound is returned when no stored analysis matches.
var ErrRecordNotFound = errors.New("analysis not found")

// safeID guards path construction: IDs come from the URL, and a SteamID or
// analysis ID that isn't plain alphanumerics has no business becoming a path.
var safeID = regexp.MustCompile(`^[0-9a-zA-Z]{1,64}$`)

// Analyses persists analyses as one JSON file per match, filed under the
// player's SteamID:
//
//	<dir>/players/<steamId>/<analysisId>.json
//
// A directory per player makes "every match of this player" a readdir, which is
// all the indexing this needs at the scale of a few matches per player. Swapping
// in a database later means reimplementing Save, Get and ByPlayer.
type Analyses struct {
	dir string
}

// NewAnalyses returns a store rooted at dir, creating it if needed.
func NewAnalyses(dir string) (*Analyses, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("creating data directory: %w", err)
	}

	return &Analyses{dir: dir}, nil
}

// Save writes a record, replacing any earlier version of the same analysis.
func (a *Analyses) Save(r *Record) error {
	if !safeID.MatchString(r.SteamID) || !safeID.MatchString(r.ID) {
		return fmt.Errorf("refusing to store record with unsafe ids %q/%q", r.SteamID, r.ID)
	}

	dir := filepath.Join(a.dir, "players", r.SteamID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating player directory: %w", err)
	}

	buf, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encoding record: %w", err)
	}

	path := filepath.Join(dir, r.ID+".json")

	// Write to a temp file and rename, so a crash mid-write can't leave a
	// half-written record that fails to parse forever after.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return fmt.Errorf("writing record: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("publishing record: %w", err)
	}

	return nil
}

// Get returns one stored analysis.
func (a *Analyses) Get(steamID, id string) (*Record, error) {
	if !safeID.MatchString(steamID) || !safeID.MatchString(id) {
		return nil, ErrRecordNotFound
	}

	return a.read(filepath.Join(a.dir, "players", steamID, id+".json"))
}

// Find locates an analysis by ID alone, scanning every player directory. The
// analysis endpoints address records by ID without the SteamID, and at this
// scale a scan is cheaper than maintaining an index.
func (a *Analyses) Find(id string) (*Record, error) {
	if !safeID.MatchString(id) {
		return nil, ErrRecordNotFound
	}

	players, err := os.ReadDir(filepath.Join(a.dir, "players"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrRecordNotFound
		}

		return nil, fmt.Errorf("listing players: %w", err)
	}

	for _, p := range players {
		if !p.IsDir() {
			continue
		}

		r, err := a.read(filepath.Join(a.dir, "players", p.Name(), id+".json"))
		if err == nil {
			return r, nil
		}

		if !errors.Is(err, ErrRecordNotFound) {
			return nil, err
		}
	}

	return nil, ErrRecordNotFound
}

// ByPlayer returns every stored analysis for a player, most recent first.
func (a *Analyses) ByPlayer(steamID string) ([]*Record, error) {
	if !safeID.MatchString(steamID) {
		return nil, nil
	}

	dir := filepath.Join(a.dir, "players", steamID)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}

		return nil, fmt.Errorf("listing analyses: %w", err)
	}

	out := make([]*Record, 0, len(entries))

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}

		r, err := a.read(filepath.Join(dir, e.Name()))
		if err != nil {
			// One unreadable file shouldn't hide a player's whole history.
			continue
		}

		out = append(out, r)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].AnalyzedAt.After(out[j].AnalyzedAt) })

	return out, nil
}

func (a *Analyses) read(path string) (*Record, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrRecordNotFound
		}

		return nil, fmt.Errorf("reading record: %w", err)
	}

	var r Record
	if err := json.Unmarshal(buf, &r); err != nil {
		return nil, fmt.Errorf("decoding record %s: %w", filepath.Base(path), err)
	}

	return &r, nil
}
