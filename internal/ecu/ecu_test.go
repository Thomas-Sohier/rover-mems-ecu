package ecu

import (
	"slices"
	"testing"
)

// TestLogDebugUnderStateLock guards the invariant that the logging mutex is
// distinct from the data mutex: every ECU parser logs while holding the State
// lock, and a shared non-reentrant mutex would deadlock the whole agent as soon
// as debug mode is enabled. A regression here hangs rather than fails, so the
// package is expected to be run with the usual `go test` timeout.
func TestLogDebugUnderStateLock(t *testing.T) {
	s := NewState()
	s.DebugMode = true

	s.Lock()
	s.LogDebug("holding the write lock")
	s.LogDebugf("holding the write lock: %d", 42)
	s.Unlock()

	s.RLock()
	s.LogDebug("holding the read lock")
	s.RUnlock()

	if got := len(s.LogLinesCopy()); got != 3 {
		t.Errorf("LogLinesCopy() length = %d, want 3", got)
	}
}

func TestLogLinesBounded(t *testing.T) {
	s := NewState()
	s.DebugMode = true

	for i := 0; i < maxLogLines*3; i++ {
		s.LogDebugf("line %d", i)
	}

	lines := s.LogLinesCopy()
	if len(lines) != maxLogLines {
		t.Fatalf("LogLinesCopy() length = %d, want %d", len(lines), maxLogLines)
	}
	if want := "line 299"; lines[len(lines)-1] != want {
		t.Errorf("last line = %q, want %q", lines[len(lines)-1], want)
	}
}

// TestLogLinesCopyIsIndependent checks callers cannot observe later mutations
// through the returned slice.
func TestLogLinesCopyIsIndependent(t *testing.T) {
	s := NewState()
	s.DebugMode = true
	s.LogDebug("first")

	got := s.LogLinesCopy()
	s.LogDebug("second")

	if !slices.Equal(got, []string{"first"}) {
		t.Errorf("LogLinesCopy() = %v, want [first]", got)
	}
}

func TestSnapshotIncludesLogLines(t *testing.T) {
	s := NewState()
	s.DebugMode = true
	s.LogDebug("hello")

	if got := s.Snapshot().LogLines; !slices.Equal(got, []string{"hello"}) {
		t.Errorf("Snapshot().LogLines = %v, want [hello]", got)
	}
}
