// Package store keeps uploaded demos and the analysis produced from them. The
// in-memory implementation is a placeholder for a real database.
package store

import (
	"errors"
	"sync"
	"time"

	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/demo"
)

// State is where a demo sits in the upload → choose → analyze flow.
type State string

const (
	// StatePending means the roster is known and the file is waiting for a
	// player to be chosen.
	StatePending State = "pending"
	// StateAnalyzing means a parse is in flight; the file is claimed.
	StateAnalyzing State = "analyzing"
	// StateAnalyzed means the analysis is done and the file has been deleted.
	StateAnalyzed State = "analyzed"
)

var (
	// ErrNotFound is returned for an unknown demo ID.
	ErrNotFound = errors.New("demo not found")
	// ErrNotPending is returned when a demo can no longer be analyzed, either
	// because a parse is already running or because the file is gone.
	ErrNotPending = errors.New("demo is not awaiting analysis")
)

// Entry is one uploaded demo and whatever is known about it.
type Entry struct {
	ID         string
	FileName   string
	State      State
	Roster     *demo.Roster
	Analysis   *demo.Analysis
	UploadedAt time.Time

	// path is the demo on disk. It is emptied once the file is consumed, which
	// is what makes the analysis a one-shot per upload.
	path string
}

// Memory is a concurrency-safe, process-lifetime store of demos.
// Insertion order is preserved so List can return newest first.
type Memory struct {
	mu    sync.Mutex
	items map[string]*Entry
	order []string
}

// NewMemory returns an empty store.
func NewMemory() *Memory {
	return &Memory{items: make(map[string]*Entry)}
}

// Put registers a freshly uploaded demo, waiting for a player to be chosen.
func (m *Memory) Put(id, fileName, path string, roster *demo.Roster) *Entry {
	m.mu.Lock()
	defer m.mu.Unlock()

	e := &Entry{
		ID:         id,
		FileName:   fileName,
		State:      StatePending,
		Roster:     roster,
		UploadedAt: time.Now().UTC(),
		path:       path,
	}

	if _, exists := m.items[id]; !exists {
		m.order = append(m.order, id)
	}

	m.items[id] = e

	return e.copy()
}

// Get returns a snapshot of the demo with the given ID.
func (m *Memory) Get(id string) (*Entry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.items[id]
	if !ok {
		return nil, false
	}

	return e.copy(), true
}

// List returns a snapshot of every demo, most recently uploaded first.
func (m *Memory) List() []*Entry {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]*Entry, 0, len(m.order))
	for i := len(m.order) - 1; i >= 0; i-- {
		out = append(out, m.items[m.order[i]].copy())
	}

	return out
}

// Claim takes exclusive ownership of a pending demo's file and returns its
// path. Only one caller can hold a claim, so two concurrent requests can't
// parse — or delete — the same file.
//
// The caller must follow up with Complete or Release.
func (m *Memory) Claim(id string) (path string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.items[id]
	if !ok {
		return "", ErrNotFound
	}

	if e.State != StatePending || e.path == "" {
		return "", ErrNotPending
	}

	e.State = StateAnalyzing

	return e.path, nil
}

// Release hands a claim back after a failed parse, so the demo can be retried.
func (m *Memory) Release(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if e, ok := m.items[id]; ok && e.State == StateAnalyzing {
		e.State = StatePending
	}
}

// Complete stores the analysis and forgets the file path, marking the upload as
// spent. Deleting the file itself is the caller's job.
func (m *Memory) Complete(id string, analysis *demo.Analysis) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.items[id]
	if !ok {
		return
	}

	e.State = StateAnalyzed
	e.Analysis = analysis
	e.path = ""
}

// Paths returns the files still on disk, so they can be cleaned up on shutdown.
func (m *Memory) Paths() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []string

	for _, e := range m.items {
		if e.path != "" {
			out = append(out, e.path)
		}
	}

	return out
}

// copy returns a shallow clone, so callers outside the lock can't mutate the
// stored entry. The Roster and Analysis pointers are shared but never written
// to after they are set.
func (e *Entry) copy() *Entry {
	clone := *e

	return &clone
}
