package serial

import (
	"runtime"
	"testing"
	"time"
)

// stubPort serves a fixed byte stream, then blocks on timeouts like an idle
// real port would.
type stubPort struct {
	data []byte
}

type timeoutErr struct{}

func (timeoutErr) Error() string { return "timeout" }
func (timeoutErr) Timeout() bool { return true }

func (p *stubPort) Read(b []byte) (int, error) {
	if len(p.data) == 0 {
		time.Sleep(time.Millisecond)
		return 0, timeoutErr{}
	}
	n := copy(b, p.data)
	p.data = p.data[n:]
	return n, nil
}

func (p *stubPort) Write(b []byte) (int, error)        { return len(b), nil }
func (p *stubPort) SetReadTimeout(time.Duration) error { return nil }
func (p *stubPort) Break(time.Duration) error          { return nil }
func (p *stubPort) Close() error                       { return nil }

// waitFor polls until cond holds or the deadline passes.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func TestReaderDeliversBytes(t *testing.T) {
	r := NewReader()
	r.Start(&stubPort{data: []byte{1, 2, 3}})
	defer r.Stop()

	var got []byte
	waitFor(t, func() bool {
		got = append(got, r.Read()...)
		return len(got) == 3
	})

	if string(got) != string([]byte{1, 2, 3}) {
		t.Errorf("got % X, want 01 02 03", got)
	}
}

func TestReaderStopIsIdempotent(t *testing.T) {
	r := NewReader()
	r.Start(&stubPort{})
	r.Stop()
	r.Stop() // must not panic on a double close
}

// TestReaderStopBeforeStart covers Stop on a Reader that was never started;
// its done channel is still nil.
func TestReaderStopBeforeStart(t *testing.T) {
	NewReader().Stop()
}

// TestReaderRestartDiscardsStaleBytes covers the restart path: bytes buffered
// from the previous port must not leak into the new session, where they would
// corrupt frame alignment.
func TestReaderRestartDiscardsStaleBytes(t *testing.T) {
	r := NewReader()

	r.Start(&stubPort{data: []byte{0xAA, 0xBB, 0xCC}})
	waitFor(t, func() bool { return len(r.channel) == 3 })
	r.Stop()

	r.Start(&stubPort{data: []byte{0x11}})
	defer r.Stop()

	var got []byte
	waitFor(t, func() bool {
		got = append(got, r.Read()...)
		return len(got) == 1
	})

	if got[0] != 0x11 {
		t.Errorf("got %#x, want 0x11 — stale bytes leaked across restart", got[0])
	}
}

// TestReaderStopEndsGoroutine checks Stop actually retires the read goroutine
// rather than leaving it spinning on a closed port.
func TestReaderStopEndsGoroutine(t *testing.T) {
	time.Sleep(50 * time.Millisecond) // let earlier tests' goroutines settle
	before := runtime.NumGoroutine()

	r := NewReader()
	r.Start(&stubPort{})
	r.Stop()

	waitFor(t, func() bool { return runtime.NumGoroutine() <= before })
}
