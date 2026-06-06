package mems1x

import (
	"context"
	"fmt"

	"rover-mems-agent/internal/ecu"

	"github.com/distributed/sers"
)

func init() {
	ecu.Register("1.x", NewMEMS1x)
}

// MEMS1x handles MEMS 1.2, 1.3, 1.6 ECUs.
type MEMS1x struct {
	state         *ecu.State
	sp            sers.SerialPort
	gotKlineEcho  bool
	lastKlineByte byte
}

// NewMEMS1x creates a new MEMS 1.x ECU handler.
func NewMEMS1x(state *ecu.State, cfg ecu.Config) (ecu.ECU, error) {
	state.DebugMode = cfg.DebugMode
	return &MEMS1x{state: state}, nil
}

func (m *MEMS1x) Connect(_ context.Context, portName string) error {
	m.state.LogDebug("Connecting to MEMS 1.x (1.2, 1.3, 1.6) ECU")
	m.state.Lock()
	m.state.Connected = false
	m.state.Unlock()

	sp, err := sers.Open(portName)
	if err != nil {
		return fmt.Errorf("open serial port %s: %w", portName, err)
	}
	m.sp = sp

	err = sp.SetMode(9600, 8, sers.N, 1, sers.NO_HANDSHAKE)
	if err != nil {
		sp.Close()
		return fmt.Errorf("set serial mode: %w", err)
	}

	err = sp.SetReadParams(0, 0.001)
	if err != nil {
		sp.Close()
		return err
	}

	mode, _ := sp.GetMode()
	m.state.LogDebug("Serial cable set to:")
	m.state.LogDebug(mode)
	return nil
}

func (m *MEMS1x) ReadData(ctx context.Context) error {
	_, err := m.loop(ctx, true)
	return err
}

func (m *MEMS1x) Close() error {
	m.state.Lock()
	m.state.Connected = false
	m.state.Unlock()
	if m.sp != nil {
		return m.sp.Close()
	}
	return nil
}

func (m *MEMS1x) Type() string {
	return "1.x"
}

// SetSerialPort sets the serial port (used by MEMS19 after its custom wake-up).
func (m *MEMS1x) SetSerialPort(sp sers.SerialPort) {
	m.sp = sp
}
