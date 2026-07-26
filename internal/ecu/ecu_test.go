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

func TestLogLinesSince(t *testing.T) {
	s := NewState()
	s.DebugMode = true
	s.LogDebug("one")
	s.LogDebug("two")

	lines, cursor := s.LogLinesSince(0)
	if !slices.Equal(lines, []string{"one", "two"}) {
		t.Fatalf("LogLinesSince(0) = %v, want [one two]", lines)
	}

	// Nothing new: no lines, cursor unchanged.
	lines, cursor2 := s.LogLinesSince(cursor)
	if len(lines) != 0 {
		t.Errorf("LogLinesSince(cursor) = %v, want empty", lines)
	}
	if cursor2 != cursor {
		t.Errorf("cursor moved without new lines: %d -> %d", cursor, cursor2)
	}

	// Only the delta comes back.
	s.LogDebug("three")
	lines, _ = s.LogLinesSince(cursor)
	if !slices.Equal(lines, []string{"three"}) {
		t.Errorf("LogLinesSince(cursor) = %v, want [three]", lines)
	}
}

// TestLogLinesSince_StaleCursor covers a consumer that fell so far behind the
// ring dropped lines it never saw: it must resync to the oldest retained line
// rather than panic on a negative slice index.
func TestLogLinesSince_StaleCursor(t *testing.T) {
	s := NewState()
	s.DebugMode = true
	for i := 0; i < maxLogLines*2; i++ {
		s.LogDebugf("line %d", i)
	}

	lines, cursor := s.LogLinesSince(0)
	if len(lines) != maxLogLines {
		t.Fatalf("stale cursor returned %d lines, want %d", len(lines), maxLogLines)
	}
	if lines[0] != "line 100" {
		t.Errorf("oldest retained line = %q, want %q", lines[0], "line 100")
	}
	if cursor != int64(maxLogLines*2) {
		t.Errorf("cursor = %d, want %d", cursor, maxLogLines*2)
	}
}

// TestLogLinesSince_FutureCursor guards against a cursor ahead of the sequence
// (a stale client reconnecting after a restart) slicing out of range.
func TestLogLinesSince_FutureCursor(t *testing.T) {
	s := NewState()
	s.DebugMode = true
	s.LogDebug("only")

	lines, cursor := s.LogLinesSince(9999)
	if len(lines) != 0 {
		t.Errorf("LogLinesSince(future) = %v, want empty", lines)
	}
	if cursor != 1 {
		t.Errorf("cursor = %d, want 1", cursor)
	}
}
