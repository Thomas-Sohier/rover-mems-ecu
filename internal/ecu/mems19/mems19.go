package mems19

import (
	"context"
	"errors"
	"fmt"
	"time"

	"rover-mems-agent/internal/ecu"
	"rover-mems-agent/internal/ecu/mems1x"

	"github.com/distributed/sers"
)

func init() {
	ecu.Register("1.9", NewMEMS19)
}

// openPort is the serial-port opener. It is a package variable so tests can
// substitute a fake SerialPort in place of a real hardware port.
var openPort = sers.Open

// sleep is time.Sleep indirected through a package variable so tests can
// neutralise the real-time delays of the 5-baud wake-up and handshake.
var sleep = time.Sleep

// MEMS19 handles MEMS 1.9 ECUs which require ISO 9141 5-baud wake-up.
type MEMS19 struct {
	*mems1x.MEMS1x
	state *ecu.State
	sp    sers.SerialPort
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

	sp, err := openPort(portName)
	if err != nil {
		return fmt.Errorf("open serial port %s: %w", portName, err)
	}
	m.sp = sp

	err = sp.SetMode(9600, 8, sers.N, 1, sers.NO_HANDSHAKE)
	if err != nil {
		sp.Close()
		return fmt.Errorf("set serial mode: %w", err)
	}

	sp.SetReadParams(1, 0.5)
	m.flushInput()

	m.send5BaudWakeup()

	err = m.handleWakeUpHandshake()
	if err != nil {
		sp.Close()
		return err
	}

	sp.SetReadParams(0, 0.001)
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
// Leading 0x00 bytes are skipped because the line transition out of the break
// condition can clock in spurious framing/zero bytes. The 0x7C default is a
// fall-back for the common KW2=0x83 case (0x83 ^ 0xFF = 0x7C) if we never see a
// clean 0x55 frame within the timeout.
func (m *MEMS19) handleWakeUpHandshake() error {
	buffer := make([]byte, 0)
	tmp := make([]byte, 128)

	challengeResponse := byte(0x7C)

	start := time.Now()
	for time.Since(start) < 2000*time.Millisecond {
		n, err := m.sp.Read(tmp)
		if err != nil {
			return err
		}
		if n > 0 {
			buffer = append(buffer, tmp[:n]...)
			m.state.LogDebug(buffer)

			for len(buffer) > 0 && buffer[0] == 0x00 {
				buffer = buffer[1:]
			}

			if len(buffer) >= 3 && buffer[0] == 0x55 {
				kw1, kw2 := buffer[1], buffer[2]
				m.state.LogDebugf("1.9 ECU Woke Response received (55 %02X %02X)", kw1, kw2)
				challengeResponse = kw2 ^ 0xFF
				break
			}
		}
	}

	if challengeResponse == 0x7C {
		m.state.LogDebug("1.9 ECU: sending challenge 0x7C (default or derived from KW2=0x83)")
	}

	sleep(25 * time.Millisecond)

	m.state.LogDebugf("Sending Challenge Response: 0x%02X", challengeResponse)
	_, err := m.sp.Write([]byte{challengeResponse})
	if err != nil {
		return err
	}

	return m.waitForChallengeEcho(challengeResponse)
}

// waitForChallengeEcho waits for the ECU to acknowledge the keyword handshake.
//
// After we send ~KW2, ISO 9141-2 has the ECU reply with the complement of its
// address byte: ~0x16 = 0xE9. That 0xE9 is what confirms the link is up.
//
// On a single-wire K-line the interface usually echoes everything we transmit,
// so the bytes we read back are [our ~KW2 echo, 0xE9]. Some USB/K-line adapters
// suppress the TX echo, in which case we only see [0xE9]. We accept either form
// so the handshake works across both adapter types.
func (m *MEMS19) waitForChallengeEcho(expectedEcho byte) error {
	buffer := make([]byte, 0)
	tmp := make([]byte, 128)

	start := time.Now()
	for time.Since(start) < 2000*time.Millisecond {
		n, err := m.sp.Read(tmp)
		if err != nil {
			return err
		}
		if n > 0 {
			buffer = append(buffer, tmp[:n]...)

			for len(buffer) > 0 && buffer[0] == 0x00 {
				buffer = buffer[1:]
			}

			if len(buffer) >= 2 && buffer[0] == expectedEcho && buffer[1] == 0xE9 {
				m.state.LogDebug("1.9 ECU init handshake complete (with TX echo)")
				return nil
			}
			if len(buffer) >= 1 && buffer[0] == 0xE9 {
				m.state.LogDebug("1.9 ECU init handshake complete (no TX echo)")
				return nil
			}
		}
	}
	return errors.New("timeout waiting for challenge echo (0xE9)")
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
// There is no UART mode for 5 baud, so we toggle the line by hand with SetBreak.
// On K-line the idle (break off) state is logic 1 and break on is logic 0, and
// 5 baud means each bit lasts 1/5 s = 200 ms. The frame is one start bit (0),
// the 8 bits of 0x16 sent LSB-first, then the stop bit (1):
//
//	500 ms idle      -> let the line settle before we begin
//	200 ms break on  -> start bit (logic 0)
//	8 x 200 ms       -> address 0x16, least-significant bit first
//	200 ms break off -> stop bit (logic 1)
//
// 0x16 is the published diagnostic address for the 1.9 ECU; sending it at this
// rate is what makes the ECU start the keyword handshake handled above.
func (m *MEMS19) send5BaudWakeup() {
	m.sp.SetBreak(false)
	sleep(500 * time.Millisecond)

	m.sp.SetBreak(true)
	sleep(200 * time.Millisecond)

	ecuAddress := 0x16
	for i := 0; i < 8; i++ {
		bit := (ecuAddress >> i) & 1
		if bit > 0 {
			m.sp.SetBreak(false)
		} else {
			m.sp.SetBreak(true)
		}
		sleep(200 * time.Millisecond)
	}

	m.sp.SetBreak(false)
	sleep(200 * time.Millisecond)
}

func (m *MEMS19) Type() string {
	return "1.9"
}
