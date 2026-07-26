package serial

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// capturePath is the file every subsequent Open mirrors its byte-level traffic
// into, or "" when capture is disabled.
//
// It is process-global rather than plumbed through the ECU handlers on purpose:
// Open is reached from six handlers and a capture that only covers some of them
// is worse than none. It is written once during flag parsing, before any
// goroutine that could Open a port exists.
var capturePath string

// EnableCapture makes every subsequent Open wrap its port in a tracing decorator
// that appends to path. Passing "" disables capture again. Call it before
// starting anything that opens ports.
func EnableCapture(path string) { capturePath = path }

// CaptureEnabled reports whether Open is currently tracing.
func CaptureEnabled() bool { return capturePath != "" }

// flushInterval bounds how much of the trace a kill -9 can cost. Writes go
// through a bufio.Writer so that formatting the trace cannot stall a serial
// read, but a buffer that is only flushed on Close would lose the whole session
// exactly when it is most interesting.
const flushInterval = time.Second

// capturePort decorates a Port, appending a timestamped line per transfer.
//
// Timestamps are taken as soon as the underlying call returns and before any
// formatting or file I/O, so the trace records when a byte actually arrived
// rather than when we got round to writing it down. That matters: the traces
// exist mainly to check ISO 9141 timings (the 5-baud bit periods, the 25–50 ms
// W4 window) that are of the same order as a careless log line.
type capturePort struct {
	Port

	mu        sync.Mutex
	f         *os.File
	w         *bufio.Writer
	start     time.Time
	lastFlush time.Time
}

func newCapturePort(p Port, path, name string, baud int, parity Parity) (Port, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open capture file %s: %w", path, err)
	}

	now := time.Now()
	c := &capturePort{
		Port:      p,
		f:         f,
		w:         bufio.NewWriterSize(f, 64<<10),
		start:     now,
		lastFlush: now,
	}

	mode := "8N1"
	if parity == EvenParity {
		mode = "8E1"
	}
	// Sessions append, so mark where each one starts. The leading column of
	// every line below is milliseconds since this header, to microsecond
	// resolution.
	fmt.Fprintf(c.w, "\n=== open %s %d %s at %s\n", name, baud, mode, now.Format(time.RFC3339Nano))
	return c, nil
}

// event appends one trace line. at is measured from the session header.
func (c *capturePort) event(at time.Duration, kind, detail string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(c.w, "%12.3f %-5s %s\n", float64(at.Microseconds())/1000, kind, detail)
	if now := time.Now(); now.Sub(c.lastFlush) >= flushInterval {
		c.lastFlush = now
		_ = c.w.Flush()
	}
}

// Read records only reads that produced bytes. The MEMS 1.x loop polls a
// non-blocking port every 10 ms, so logging empty reads would bury the traffic
// in a hundred lines a second of nothing.
func (c *capturePort) Read(p []byte) (int, error) {
	n, err := c.Port.Read(p)
	at := time.Since(c.start)
	if n > 0 {
		c.event(at, "RX", hexBytes(p[:n]))
	}
	// A timed-out read is the idle line, not an error worth a line of trace.
	if err != nil && !isTimeout(err) {
		c.event(at, "ERR", "read: "+err.Error())
	}
	return n, err
}

func (c *capturePort) Write(p []byte) (int, error) {
	n, err := c.Port.Write(p)
	at := time.Since(c.start)
	c.event(at, "TX", hexBytes(p[:n]))
	if err != nil {
		c.event(at, "ERR", "write: "+err.Error())
	}
	return n, err
}

// Break records the requested pulse width alongside the width actually
// achieved. The 5-baud wake-up bit-bangs 200 ms bit periods out of these calls,
// so the gap between the two is the direct measure of whether the wake-up
// waveform is still within what the ECU will accept.
func (c *capturePort) Break(d time.Duration) error {
	begin := time.Since(c.start)
	err := c.Port.Break(d)
	end := time.Since(c.start)

	c.event(begin, "BRK", fmt.Sprintf("low for %v requested, %.3fms actual", d, float64((end-begin).Microseconds())/1000))
	if err != nil {
		c.event(end, "ERR", "break: "+err.Error())
	}
	// Breaks only happen during wake-up, so flushing here is free and puts the
	// most diagnostic part of the trace on disk immediately.
	c.flush()
	return err
}

func (c *capturePort) SetReadTimeout(t time.Duration) error {
	err := c.Port.SetReadTimeout(t)
	c.event(time.Since(c.start), "CFG", "read timeout "+t.String())
	return err
}

func (c *capturePort) Close() error {
	err := c.Port.Close()
	c.event(time.Since(c.start), "CLOSE", "")

	c.mu.Lock()
	defer c.mu.Unlock()
	return errors.Join(err, c.w.Flush(), c.f.Close())
}

func (c *capturePort) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastFlush = time.Now()
	_ = c.w.Flush()
}

// hexBytes renders bytes as space-separated uppercase hex, the form the ECU
// documentation uses.
func hexBytes(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b) * 3)
	for i, x := range b {
		if i > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "%02X", x)
	}
	return sb.String()
}

// isTimeout reports whether err is a read deadline elapsing rather than a real
// failure, using the same duck-typed check as the ECU read loops.
func isTimeout(err error) bool {
	var te interface{ Timeout() bool }
	return errors.As(err, &te) && te.Timeout()
}
