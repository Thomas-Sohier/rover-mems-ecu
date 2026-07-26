package mems19

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"rover-mems-agent/internal/ecu"
	"rover-mems-agent/internal/ecu/mems1x"
	"rover-mems-agent/internal/serial"
)

func init() {
	ecu.Register("1.9", NewMEMS19)
}

// openPort is the serial-port opener. It is a package variable so tests can
// substitute a fake Port in place of a real hardware port.
var openPort = serial.Open

// sleep is time.Sleep indirected through a package variable so tests can
// neutralise the real-time delays of the 5-baud wake-up and handshake.
var sleep = time.Sleep

const (
	// w4Delay is the ISO 9141-2 pause between receiving KW2 and replying with
	// ~KW2. The standard allows 25–50 ms; we sit in the middle rather than on
	// the 25 ms floor because on an FTDI adapter we do not learn about KW2 the
	// instant it lands. The chip holds short packets for its latency timer
	// (16 ms out of the box) before forwarding them upstream, so part of the
	// window is already spent by the time we are woken. If the handshake proves
	// marginal on real hardware, lower the adapter's latency timer first:
	//
	//	echo 1 | sudo tee /sys/bus/usb-serial/devices/ttyUSB0/latency_timer
	w4Delay = 30 * time.Millisecond

	// p3Delay is the ISO 9141-2 gap between the end of initialisation and the
	// first request of the session (P3, minimum 55 ms). Without it the shared
	// mems1x loop fires 0xCA at an ECU that has not finished settling, and the
	// byte is simply dropped.
	p3Delay = 60 * time.Millisecond
)

// MEMS19 handles MEMS 1.9 ECUs which require ISO 9141 5-baud wake-up.
type MEMS19 struct {
	*mems1x.MEMS1x
	state *ecu.State
	sp    serial.Port
}

// NewMEMS19 creates a new MEMS 1.9 ECU handler.
func NewMEMS19(state *ecu.State, cfg ecu.Config) (ecu.ECU, error) {
	state.DebugMode = cfg.DebugMode
	base, err := mems1x.NewMEMS1x(state, cfg)
	if err != nil {
		return nil, err
	}
	base1x, ok := base.(*mems1x.MEMS1x)
	if !ok {
		return nil, fmt.Errorf("unexpected base ECU type %T", base)
	}
	return &MEMS19{
		MEMS1x: base1x,
		state:  state,
	}, nil
}

// Connect performs the full MEMS 1.9 wake-up, which the 1.x ECUs do not need.
//
// Per the rovermems 1.9 notes (https://rovermems.com/mems-1.9/index.html), the
// 1.9 ECU stays silent until it receives an ISO 9141 5-baud slow-init carrying
// its address (0x16). After that handshake it behaves exactly like a 1.3/1.6
// ECU, so once we are woken up we hand the already-configured serial port to the
// shared mems1x handler and run its normal CA/75/D0/80 loop.
//
// Sequence: open at 9600 8N1, drain any stale bytes, bit-bang the 5-baud
// address, run the keyword handshake, then drop the read timeout back to
// non-blocking for the fast data loop.
func (m *MEMS19) Connect(_ context.Context, portName string) error {
	m.state.LogDebug("Connecting to MEMS 1.9 ECU")
	m.state.Lock()
	m.state.Connected = false
	m.state.Unlock()

	sp, err := openPort(portName, 9600, serial.NoParity)
	if err != nil {
		return fmt.Errorf("open serial port %s: %w", portName, err)
	}
	m.state.LogDebugf("1.9 serial port %s opened at 9600 8N1", portName)
	m.sp = sp

	if err = sp.SetReadTimeout(500 * time.Millisecond); err != nil {
		sp.Close()
		return err
	}
	m.state.LogDebug("1.9 flushing stale input before wake-up")
	m.flushInput()

	m.state.LogDebug("1.9 sending 5-baud slow-init wake-up (address 0x16)")
	if err := m.send5BaudWakeup(); err != nil {
		sp.Close()
		return err
	}
	m.state.LogDebug("1.9 5-baud wake-up sent, starting keyword handshake")

	// The handshake is best-effort : initEcu discards
	// wakeUp19Ecu's return value and falls through to the 0xCA init regardless of
	// whether the keyword/echo exchange actually completed. Some K-line adapters
	// buffer the echo differently or clock in a stray byte before 0x7C, which
	// would otherwise abort an ECU that is in fact awake. So we log and proceed.
	if err := m.handleWakeUpHandshake(); err != nil {
		m.state.LogDebugf("1.9 wake-up handshake did not complete cleanly: %v (continuing to 0xCA init anyway)", err)
	} else {
		m.state.LogDebug("1.9 wake-up handshake completed cleanly")
	}

	// Drain the handshake leftovers (our 0x7C echo, the ECU's 0xE9) so the shared
	// mems1x loop's K-line echo tracking starts clean on its own 0xCA
	if err := sp.SetReadTimeout(0); err != nil {
		sp.Close()
		return err
	}
	m.flushInput()

	m.state.LogDebugf("1.9 waiting P3 (%v) before the first request", p3Delay)
	sleep(p3Delay)

	m.state.LogDebug("1.9 handing off port to shared mems1x data loop (0xCA init)")
	m.MEMS1x.SetSerialPort(sp)
	return nil
}

// handleWakeUpHandshake performs the ISO 9141-2 keyword exchange that follows
// the 5-baud address.
//
// After the slow-init the ECU answers (now at 9600 baud) with a sync byte 0x55
// followed by two keyword bytes KW1, KW2. ISO 9141-2 requires the tester to
// reply with the bitwise complement of the second keyword byte (~KW2) — the
// rovermems 1.9 page phrases this as "invert second byte in ECU reply". Sending
// that back tells the ECU we understood its keywords and unlocks the session
// (there is no further authentication on 1.9).
//
// The 0x7C default is a fall-back for the common KW2=0x83 case
// (0x83 ^ 0xFF = 0x7C) if we never see a keyword frame within the timeout.
func (m *MEMS19) handleWakeUpHandshake() error {
	challengeResponse := byte(0x7C)

	buf, ok, err := m.readUntil("handshake", 2*time.Second, func(b []byte) bool {
		return keywordFrame(b) != nil
	})
	if err != nil {
		return err
	}
	if ok {
		kw := keywordFrame(buf)
		m.state.LogDebugf("1.9 ECU Woke Response received (55 %02X %02X)", kw[0], kw[1])
		challengeResponse = kw[1] ^ 0xFF
	} else {
		m.state.LogDebug("1.9 ECU: no keyword frame within timeout; sending fallback challenge 0x7C (assumes KW2=0x83)")
	}

	// Log before sleeping, never between the sleep and the Write: W4 is a
	// 25–50 ms window and a debug line going to a slow console (a serial or SSH
	// terminal on the Pi) is easily worth several milliseconds of it.
	m.state.LogDebugf("Sending Challenge Response: 0x%02X after W4 (%v)", challengeResponse, w4Delay)
	sleep(w4Delay)
	if _, err := m.sp.Write([]byte{challengeResponse}); err != nil {
		return err
	}

	return m.waitForChallengeEcho()
}

// keywordFrame returns [KW1, KW2] if b holds the ISO 9141-2 sync byte 0x55
// followed by two further bytes, or nil if it does not (yet).
//
// The frame is searched for rather than expected at b[0]. On a single-wire
// K-line our own bit-banged 5-baud address is echoed straight back at us, and
// the UART sees those 200 ms line states as a run of framing errors. Which byte
// value a driver reports for a framing error is not portable — 0x00 commonly,
// but 0xF8/0xFE and other junk depending on adapter and driver. Anchoring the
// match on b[0] meant a single non-zero junk byte masked the real handshake for
// good and dropped us onto the blind 0x7C fallback.
func keywordFrame(b []byte) []byte {
	for i, v := range b {
		if v == 0x55 && len(b) >= i+3 {
			return b[i+1 : i+3]
		}
	}
	return nil
}

// waitForChallengeEcho waits for the ECU to acknowledge the keyword handshake.
//
// After we send ~KW2, ISO 9141-2 has the ECU reply with the complement of its
// address byte: ~0x16 = 0xE9. That 0xE9 is what confirms the link is up.
//
// On a single-wire K-line the interface usually echoes everything we transmit,
// so the bytes we read back are [our ~KW2 echo, 0xE9]. Some USB/K-line adapters
// suppress the TX echo, in which case we only see [0xE9]. Rather than enumerate
// those shapes we just look for 0xE9 anywhere in what came back, which covers
// both adapter types and any leading line noise. A stray 0xE9 in that noise
// would be a false positive, but the caller treats this handshake as
// best-effort and proceeds either way, so it costs nothing.
func (m *MEMS19) waitForChallengeEcho() error {
	_, ok, err := m.readUntil("challenge-echo", 2*time.Second, func(b []byte) bool {
		return bytes.IndexByte(b, 0xE9) >= 0
	})
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("timeout waiting for challenge echo (0xE9)")
	}
	m.state.LogDebug("1.9 ECU init handshake complete")
	return nil
}

// readUntil polls the serial port until match reports success or timeout
// elapses. Before each check it strips leading 0x00 framing bytes, which the
// K-line can clock in as it leaves the break condition. It returns the
// accumulated buffer and whether match succeeded before the deadline; a read
// error aborts immediately. label names the exchange in debug traces.
func (m *MEMS19) readUntil(label string, timeout time.Duration, match func(buf []byte) bool) ([]byte, bool, error) {
	var buf []byte
	tmp := make([]byte, 128)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		n, err := m.sp.Read(tmp)
		if err != nil {
			return buf, false, err
		}
		if n == 0 {
			continue
		}
		buf = append(buf, tmp[:n]...)
		for len(buf) > 0 && buf[0] == 0x00 {
			buf = buf[1:]
		}
		m.state.LogDebugf("1.9 %s read %d byte(s), buffer now %X", label, n, buf)
		if match(buf) {
			return buf, true, nil
		}
	}
	return buf, false, nil
}

// flushInput drains any bytes sitting in the OS receive buffer before we start
// the wake-up, so a previous (failed) session's leftovers cannot be mistaken for
// the 0x55 sync byte of the new handshake.
func (m *MEMS19) flushInput() {
	buf := make([]byte, 1024)
	for {
		n, err := m.sp.Read(buf)
		if n == 0 {
			if err != nil {
				var te interface{ Timeout() bool }
				if !errors.As(err, &te) || !te.Timeout() {
					m.state.LogDebugf("serial read during flush: %v", err)
				}
			}
			break
		}
	}
}

// send5BaudWakeup bit-bangs the ECU address 0x16 as an ISO 9141 5-baud slow-init.
//
// There is no UART mode for 5 baud, so we drive the line by hand. On K-line the
// idle state is logic 1 (line high) and a break is logic 0 (line low); 5 baud
// means each bit lasts 1/5 s = 200 ms. The frame is one start bit (0), the 8
// bits of 0x16 sent LSB-first, then the stop bit (1):
//
//	500 ms idle  -> let the line settle before we begin
//	200 ms low   -> start bit (logic 0)
//	8 x 200 ms   -> address 0x16, least-significant bit first
//	200 ms high  -> stop bit (logic 1)
//
// Port.Break asserts a low pulse for a fixed duration, so we coalesce runs of
// consecutive logic-0 bits into a single Break (avoiding any line glitch between
// adjacent low bits) and runs of logic-1 bits into a single idle sleep.
//
// 0x16 is the published diagnostic address for the 1.9 ECU; sending it at this
// rate is what makes the ECU start the keyword handshake handled above.
//
// A failing Break aborts the whole Connect. Port.Break is TIOCSBRK/TIOCCBRK on
// Linux, so it fails outright on a USB adapter whose driver has no break_ctl —
// and with no break there is no wake-up at all, so every byte read afterwards
// is noise. Reporting that is the difference between "your adapter cannot do
// this" and an unexplained handshake timeout two seconds later.
func (m *MEMS19) send5BaudWakeup() error {
	const bitTime = 200 * time.Millisecond
	const ecuAddress = 0x16

	// frame: start bit (0), address LSB-first, stop bit (1).
	frame := []int{0}
	for i := range 8 {
		frame = append(frame, (ecuAddress>>i)&1)
	}
	frame = append(frame, 1)

	// Let the line settle high before the start bit.
	sleep(500 * time.Millisecond)

	for i := 0; i < len(frame); {
		j := i
		for j < len(frame) && frame[j] == frame[i] {
			j++
		}
		d := time.Duration(j-i) * bitTime
		if frame[i] == 0 {
			// hold the line low for the whole run
			if err := m.sp.Break(d); err != nil {
				return fmt.Errorf("5-baud wake-up: break for %v at bit %d: %w", d, i, err)
			}
		} else {
			sleep(d) // line idles high
		}
		i = j
	}
	return nil
}

func (m *MEMS19) Type() string {
	return "1.9"
}
