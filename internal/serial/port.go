package serial

import (
	"time"

	bugst "go.bug.st/serial"
)

// Parity selects the parity bit setting for Open. Only the values the ECUs use
// are exposed.
type Parity int

const (
	// NoParity disables parity (8N1). Used by every ECU except MEMS 3.
	NoParity Parity = iota
	// EvenParity enables even parity (8E1). Used by MEMS 3.
	EvenParity
)

// Port is the minimal serial-port surface the ECU handlers need. It is a subset
// of go.bug.st/serial.Port, which satisfies it in production; tests supply a
// fake. Keeping our own interface means the ECU packages and their tests do not
// depend on the serial library directly.
type Port interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	// SetReadTimeout sets the maximum time a Read blocks. Zero makes Read
	// non-blocking (returns immediately with whatever is buffered); a positive
	// duration blocks up to that long. On timeout Read returns (0, nil).
	SetReadTimeout(t time.Duration) error
	// Break asserts a break condition (K-line driven low, logic 0) for d, then
	// clears it (line idles high, logic 1). It blocks for d. This replaces the
	// hold-style SetBreak(true)+sleep / SetBreak(false) pattern used for the
	// 5-baud and fast-init wake-ups: a low pulse is Break(d); a high period is
	// just a sleep with no port call.
	Break(d time.Duration) error
	Close() error
}

// Open opens the named port at the given baud rate, 8 data bits, 1 stop bit and
// the requested parity. The returned Port has no read timeout configured yet;
// callers set one with SetReadTimeout.
//
// When EnableCapture has been called the port is wrapped in a decorator that
// appends a timestamped trace of every transfer to the capture file. A capture
// file that cannot be opened fails the Open: it is only ever requested
// explicitly, and silently running untraced is exactly the sort of quiet
// degradation these traces exist to find.
func Open(name string, baud int, parity Parity) (Port, error) {
	p := bugst.NoParity
	if parity == EvenParity {
		p = bugst.EvenParity
	}
	port, err := bugst.Open(name, &bugst.Mode{
		BaudRate: baud,
		DataBits: 8,
		Parity:   p,
		StopBits: bugst.OneStopBit,
	})
	if err != nil {
		return nil, err
	}
	if capturePath == "" {
		return port, nil
	}
	captured, err := newCapturePort(port, capturePath, name, baud, parity)
	if err != nil {
		port.Close()
		return nil, err
	}
	return captured, nil
}
