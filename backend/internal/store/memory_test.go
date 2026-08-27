package store_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/demo"
	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/store"
)

func TestClaimLifecycle(t *testing.T) {
	m := store.NewMemory()
	m.Put("a", "match.dem", "/tmp/a.upload", &demo.Roster{Map: "de_mirage"})

	path, err := m.Claim("a")
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}

	if path != "/tmp/a.upload" {
		t.Errorf("path = %q, want the stored one", path)
	}

	// A second claim while the first is in flight must be refused.
	if _, err := m.Claim("a"); !errors.Is(err, store.ErrNotPending) {
		t.Errorf("second claim: got %v, want ErrNotPending", err)
	}

	if e, _ := m.Get("a"); e.State != store.StateAnalyzing {
		t.Errorf("state = %q, want analyzing", e.State)
	}

	// Releasing a failed parse puts the demo back in play.
	m.Release("a")

	if _, err := m.Claim("a"); err != nil {
		t.Fatalf("claim after release: %v", err)
	}

	m.Complete("a", &demo.Analysis{})

	e, _ := m.Get("a")
	if e.State != store.StateAnalyzed {
		t.Errorf("state = %q, want analyzed", e.State)
	}

	if e.Analysis == nil {
		t.Error("analysis was not stored")
	}

	// Once analyzed the file is spent, so it can never be claimed again.
	if _, err := m.Claim("a"); !errors.Is(err, store.ErrNotPending) {
		t.Errorf("claim after complete: got %v, want ErrNotPending", err)
	}

	if paths := m.Paths(); len(paths) != 0 {
		t.Errorf("Paths returned %v, want none after completion", paths)
	}
}

func TestClaimUnknown(t *testing.T) {
	if _, err := store.NewMemory().Claim("nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// TestClaimIsExclusive checks the guarantee the handler relies on: concurrent
// requests must not both get the file, or one would parse it while the other
// deletes it.
func TestClaimIsExclusive(t *testing.T) {
	m := store.NewMemory()
	m.Put("a", "match.dem", "/tmp/a.upload", &demo.Roster{})

	const racers = 32

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		granted int
	)

	wg.Add(racers)

	for range racers {
		go func() {
			defer wg.Done()

			if _, err := m.Claim("a"); err == nil {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if granted != 1 {
		t.Fatalf("%d goroutines got the claim, want exactly 1", granted)
	}
}

func TestListIsNewestFirst(t *testing.T) {
	m := store.NewMemory()
	m.Put("a", "first.dem", "/tmp/a", &demo.Roster{})
	m.Put("b", "second.dem", "/tmp/b", &demo.Roster{})

	list := m.List()
	if len(list) != 2 {
		t.Fatalf("got %d demos, want 2", len(list))
	}

	if list[0].ID != "b" {
		t.Errorf("first entry is %q, want the newest (b)", list[0].ID)
	}

	if paths := m.Paths(); len(paths) != 2 {
		t.Errorf("Paths returned %d, want 2 pending files", len(paths))
	}
}

// TestGetReturnsSnapshot makes sure callers can't mutate stored state through
// the value they get back.
func TestGetReturnsSnapshot(t *testing.T) {
	m := store.NewMemory()
	m.Put("a", "match.dem", "/tmp/a", &demo.Roster{})

	e, _ := m.Get("a")
	e.State = store.StateAnalyzed

	if again, _ := m.Get("a"); again.State != store.StatePending {
		t.Fatalf("state = %q, want the store to be unaffected", again.State)
	}
}
