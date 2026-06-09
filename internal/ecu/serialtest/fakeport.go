// Package serialtest provides a scriptable fake implementation of
// serial.Port for use in unit tests, so K-line protocol logic can be
// exercised without real serial hardware.
package serialtest

import (
	"time"

	"rover-mems-agent/internal/serial"
)

// timeoutError is a read error that reports Timeout() == true, mimicking the
// behaviour of a real serial port whose read deadline elapsed with no data.
type timeoutError struct{}

func (timeoutError) Error() string { return "serialtest: read timeout" }
func (timeoutError) Timeout() bool { return true }

// FakePort is a scriptable serial.Port. Reads are served from a queue of
// scripted responses; everything written, and every break state, is recorded
// for assertions.
//
// The zero value is not ready to use; call NewFakePort.
type FakePort struct {
	// reads is the queue of scripted Read outcomes, consumed front-to-back.
	reads []readResult
	// readErr, when set, is returned (with n==0) once the scripted reads are
	// exhausted. When nil, an exhausted queue returns a timeout error so callers
	// that poll behave as they would against an idle real port.
	readErr error

	// Written accumulates every byte passed to Write, in order.
	Written []byte
	// Breaks records the duration of every Break call in order. Each entry is a
	// logic-0 (line-low) segment of a wake-up waveform; logic-1 segments produce
	// no Break call.
	Breaks []time.Duration
	// Closed reports whether Close has been called.
	Closed bool
}

type readResult struct {
	data []byte
	err  error
}

// NewFakePort returns a FakePort with no scripted reads. Use Enqueue/EnqueueErr
// to script the bytes the "ECU" sends back.
func NewFakePort() *FakePort {
	return &FakePort{}
}

// Enqueue scripts one Read that returns the given bytes (and a nil error).
// Each Enqueue is delivered by exactly one Read call.
func (p *FakePort) Enqueue(data ...byte) *FakePort {
	p.reads = append(p.reads, readResult{data: data})
	return p
}

// EnqueueErr scripts one Read that returns the given error with n == 0.
func (p *FakePort) EnqueueErr(err error) *FakePort {
	p.reads = append(p.reads, readResult{err: err})
	return p
}

// SetExhaustedErr sets the error returned once the scripted reads run out.
// By default an exhausted port returns a timeout error (n == 0).
func (p *FakePort) SetExhaustedErr(err error) *FakePort {
	p.readErr = err
	return p
}

// Read implements io.Reader, serving scripted responses in order.
func (p *FakePort) Read(b []byte) (int, error) {
	if len(p.reads) > 0 {
		r := p.reads[0]
		p.reads = p.reads[1:]
		if r.err != nil {
			return 0, r.err
		}
		n := copy(b, r.data)
		return n, nil
	}
	if p.readErr != nil {
		return 0, p.readErr
	}
	return 0, timeoutError{}
}

// Write implements io.Writer, recording the bytes in Written.
func (p *FakePort) Write(b []byte) (int, error) {
	p.Written = append(p.Written, b...)
	return len(b), nil
}

// Close implements io.Closer.
func (p *FakePort) Close() error {
	p.Closed = true
	return nil
}

// Break records the duration of the break pulse and returns immediately (unlike
// a real port, it does not actually block for the duration).
func (p *FakePort) Break(d time.Duration) error {
	p.Breaks = append(p.Breaks, d)
	return nil
}

// SetReadTimeout is a no-op that satisfies the interface.
func (p *FakePort) SetReadTimeout(time.Duration) error { return nil }

// Compile-time check that FakePort implements the interface.
var _ serial.Port = (*FakePort)(nil)

// NewTimeoutError returns an error whose Timeout() method reports true, useful
// for scripting an idle-line read.
func NewTimeoutError() error { return timeoutError{} }
