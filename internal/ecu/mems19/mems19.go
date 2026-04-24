package mems19

import (
	"errors"
	"time"

	"rover-mems-agent/internal/ecu"
	"rover-mems-agent/internal/ecu/mems1x"

	"github.com/distributed/sers"
)

func init() {
	ecu.Register("1.9", NewMEMS19)
}

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
	return &MEMS19{
		MEMS1x: base.(*mems1x.MEMS1x),
		state:  state,
	}, nil
}

func (m *MEMS19) Connect(portName string) error {
	m.state.LogDebug("Connecting to MEMS 1.9 ECU")

	sp, err := sers.Open(portName)
	if err != nil {
		return err
	}
	m.sp = sp

	err = sp.SetMode(9600, 8, sers.N, 1, sers.NO_HANDSHAKE)
	if err != nil {
		sp.Close()
		return err
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

	time.Sleep(25 * time.Millisecond)

	m.state.LogDebugf("Sending Challenge Response: 0x%02X", challengeResponse)
	_, err := m.sp.Write([]byte{challengeResponse})
	if err != nil {
		return err
	}

	return m.waitForChallengeEcho(challengeResponse)
}

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

func (m *MEMS19) flushInput() {
	buf := make([]byte, 1024)
	for {
		n, _ := m.sp.Read(buf)
		if n == 0 {
			break
		}
	}
}

func (m *MEMS19) send5BaudWakeup() {
	m.sp.SetBreak(false)
	time.Sleep(500 * time.Millisecond)

	m.sp.SetBreak(true)
	time.Sleep(200 * time.Millisecond)

	ecuAddress := 0x16
	for i := 0; i < 8; i++ {
		bit := (ecuAddress >> i) & 1
		if bit > 0 {
			m.sp.SetBreak(false)
		} else {
			m.sp.SetBreak(true)
		}
		time.Sleep(200 * time.Millisecond)
	}

	m.sp.SetBreak(false)
	time.Sleep(200 * time.Millisecond)
}

func (m *MEMS19) Type() string {
	return "1.9"
}
