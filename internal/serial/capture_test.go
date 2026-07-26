package serial

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// tracedPort is a minimal Port whose calls the capture decorator wraps.
type tracedPort struct {
	readData []byte
	readErr  error
	writeErr error
	breakErr error
	closed   bool
}

func (p *tracedPort) Read(b []byte) (int, error) {
	if p.readErr != nil {
		return 0, p.readErr
	}
	n := copy(b, p.readData)
	p.readData = nil
	return n, nil
}

func (p *tracedPort) Write(b []byte) (int, error) {
	if p.writeErr != nil {
		return 0, p.writeErr
	}
	return len(b), nil
}

func (p *tracedPort) SetReadTimeout(time.Duration) error { return nil }
func (p *tracedPort) Break(time.Duration) error          { return p.breakErr }
func (p *tracedPort) Close() error                       { p.closed = true; return nil }

// newCapture wires a capturePort onto a temp file and returns both, plus a
// reader for the flushed trace.
func newCapture(t *testing.T, p Port) (Port, func() string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trace.log")
	c, err := newCapturePort(p, path, "/dev/fake", 9600, NoParity)
	if err != nil {
		t.Fatalf("newCapturePort: %v", err)
	}
	return c, func() string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read trace: %v", err)
		}
		return string(b)
	}
}

func TestCaptureRecordsTransfers(t *testing.T) {
	stub := &tracedPort{readData: []byte{0x55, 0x12, 0x80}}
	c, trace := newCapture(t, stub)

	if _, err := c.Write([]byte{0xCA}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(make([]byte, 16)); err != nil {
		t.Fatal(err)
	}
	if err := c.Break(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	got := trace()
	for _, want := range []string{
		"=== open /dev/fake 9600 8N1",
		"TX    CA",
		"RX    55 12 80",
		"BRK   low for 200ms requested",
		"CLOSE",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("trace missing %q:\n%s", want, got)
		}
	}
	if !stub.closed {
		t.Error("Close did not reach the wrapped port")
	}
}

// TestCaptureSkipsIdlePolling guards the property that makes the trace usable:
// the MEMS 1.x loop polls a non-blocking port every 10 ms, so empty reads must
// not produce output or the real traffic drowns in them.
func TestCaptureSkipsIdlePolling(t *testing.T) {
	c, trace := newCapture(t, &tracedPort{readErr: timeoutErr{}})

	for range 50 {
		if _, err := c.Read(make([]byte, 16)); err == nil {
			t.Fatal("expected the stub timeout")
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	for _, line := range strings.Split(trace(), "\n") {
		if strings.Contains(line, "RX") || strings.Contains(line, "ERR") {
			t.Errorf("idle poll produced a trace line: %q", line)
		}
	}
}

func TestCaptureRecordsErrors(t *testing.T) {
	c, trace := newCapture(t, &tracedPort{
		readErr:  errors.New("input/output error"),
		breakErr: errors.New("inappropriate ioctl for device"),
	})

	// Both calls fail by design; the point is that the failure reaches the trace.
	_, _ = c.Read(make([]byte, 4))
	_ = c.Break(time.Millisecond)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	got := trace()
	if !strings.Contains(got, "ERR   read: input/output error") {
		t.Errorf("read error not traced:\n%s", got)
	}
	if !strings.Contains(got, "ERR   break: inappropriate ioctl for device") {
		t.Errorf("break error not traced:\n%s", got)
	}
}

// TestCaptureTimestampsAreMonotonic checks the leading column parses as
// milliseconds and never goes backwards, since reading protocol timings off it
// is the whole point of the file.
func TestCaptureTimestampsAreMonotonic(t *testing.T) {
	c, trace := newCapture(t, &tracedPort{})

	for i := range 5 {
		if _, err := c.Write([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	stamp := regexp.MustCompile(`^\s*(\d+\.\d{3}) `)
	prev := -1.0
	seen := 0
	for _, line := range strings.Split(trace(), "\n") {
		m := stamp.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		seen++
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			t.Fatalf("timestamp %q not a number: %v", m[1], err)
		}
		if v < prev {
			t.Errorf("timestamp went backwards: %v after %v", v, prev)
		}
		prev = v
	}
	if seen != 6 { // 5 writes + CLOSE
		t.Errorf("traced %d timestamped events, want 6", seen)
	}
}

func TestEnableCapture(t *testing.T) {
	t.Cleanup(func() { EnableCapture("") })

	if CaptureEnabled() {
		t.Fatal("capture should default to off")
	}
	EnableCapture("/tmp/whatever")
	if !CaptureEnabled() {
		t.Error("EnableCapture did not take effect")
	}
	EnableCapture("")
	if CaptureEnabled() {
		t.Error("empty path should disable capture")
	}
}

func TestNewCapturePort_UnwritablePath(t *testing.T) {
	// A path whose parent does not exist can never be created.
	bad := filepath.Join(t.TempDir(), "no-such-dir", "trace.log")
	if _, err := newCapturePort(&tracedPort{}, bad, "/dev/fake", 9600, NoParity); err == nil {
		t.Fatal("expected an error for an uncreatable capture file")
	}
}

func TestHexBytes(t *testing.T) {
	tests := []struct {
		in   []byte
		want string
	}{
		{nil, ""},
		{[]byte{0x00}, "00"},
		{[]byte{0xCA, 0x7D, 0x0F}, "CA 7D 0F"},
	}
	for _, tt := range tests {
		if got := hexBytes(tt.in); got != tt.want {
			t.Errorf("hexBytes(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
