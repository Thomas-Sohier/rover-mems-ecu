package mems3

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"rover-mems-agent/internal/ecu"
	"rover-mems-agent/internal/serial"
)

func init() {
	ecu.Register("3", NewMEMS3)
}

var (
	requestHeader = []byte{0xB8, 0x13, 0xF7}

	initCommand       = []byte{0x1A, 0x9A}
	initAccepted      = []byte{0x5A, 0x9A}
	startDiagnostic   = []byte{0x10, 0xA0}
	startDiagResponse = []byte{0x50}
	requestSeed       = []byte{0x27, 0x01}
	seedResponse      = []byte{0x67, 0x01}
	sendKey           = []byte{0x27, 0x02}
	keyAcceptResponse = []byte{0x67, 0x02}
	pingCommand       = []byte{0x3E}
	pongResponse      = []byte{0x7E}

	clearFaultsCommand    = []byte{0x14, 0x00, 0x00}
	faultsClearedResponse = []byte{0x54, 0x00, 0x00}
	requestFaultsCommand  = []byte{0x18, 0x0, 0x0, 0x0}
	responseFaults        = []byte{0x58}

	requestData00  = []byte{0x21, 0x00}
	requestData06  = []byte{0x21, 0x06}
	requestData0A  = []byte{0x21, 0x0A}
	requestData0B  = []byte{0x21, 0x0B}
	requestData21  = []byte{0x21, 0x21}
	responseData00 = []byte{0x61, 0x00}
	responseData06 = []byte{0x61, 0x06}
	responseData0A = []byte{0x61, 0x0A}
	responseData0B = []byte{0x61, 0x0B}
	responseData21 = []byte{0x61, 0x21}

	userCommands = map[string][]byte{
		"clearfaults": clearFaultsCommand,
	}

	// nextAfterResponse maps a complete ECU reply to the request that follows
	// it, stating the whole sequence in one place: init -> diag -> seed/key ->
	// ping -> faults -> data PIDs -> back to ping. The seed reply is handled
	// separately in sendNextCommand because its answer carries the derived key.
	nextAfterResponse = map[string][]byte{
		string(initAccepted):          startDiagnostic,
		string(startDiagResponse):     requestSeed,
		string(keyAcceptResponse):     pingCommand,
		string(pongResponse):          requestFaultsCommand,
		string(faultsClearedResponse): requestFaultsCommand,
		string(responseFaults):        requestData00,
		string(responseData00):        requestData06,
		string(responseData06):        requestData0A,
		string(responseData0A):        requestData0B,
		string(responseData0B):        requestData21,
		string(responseData21):        pingCommand,
	}

	faultTypes = map[int]string{
		0x20: "historical",
		0x74: "present, test not complete",
		0x30: "historical, test not complete",
		0x58: "present, test not complete",
		0x61: "present",
		0x62: "present",
		0x64: "present",
		0x71: "present, test not complete",
	}
	faults = map[int]string{
		0x1232: "fuel pump relay, open circuit",
		0x0650: "MIL control circuit malfunction",
		0x0481: "A/C condensor fan",
		0x1508: "IACV driver open circuit",
		0x1186: "front lambda heater",
		0x1185: "front lambda heater",
		0x1192: "rear lambda heater",
		0x0445: "purge valve drive",
		0x0480: "cooling fan",
		0x1610: "main relay - open circuit",
		0x0113: "IAT shorted",
		0x0118: "coolant temp sensor shorted",
		0x0122: "throttle pot shorted",
		0x0562: "system voltage malfunction",
		0x0197: "oil temp sensor shorted",
		0x0462: "fuel tank level sensor shorted to ground",
		0x0340: "cam position sensor",
		0x0106: "manifold pressure - incorrect reading",
		0x1316: "misfire causing excess emissions",
		0x0170: "fuel system",
		0x0655: "warning lamp - engine bay temperature - open circuit",
	}
)

// MEMS3 handles MEMS 3 ECUs.
type MEMS3 struct {
	state *ecu.State
	sp    serial.Port
	seed  int
	key   int
}

// NewMEMS3 creates a new MEMS 3 ECU handler.
func NewMEMS3(state *ecu.State, cfg ecu.Config) (ecu.ECU, error) {
	state.DebugMode = cfg.DebugMode
	return &MEMS3{state: state}, nil
}

func (m *MEMS3) Connect(_ context.Context, portName string) error {
	m.state.LogDebug("Connecting to MEMS 3 ECU")
	m.state.Lock()
	m.state.Connected = false
	m.state.Unlock()

	sp, err := serial.Open(portName, 9600, serial.EvenParity)
	if err != nil {
		return fmt.Errorf("open serial port %s: %w", portName, err)
	}
	m.sp = sp

	if err = sp.SetReadTimeout(0); err != nil {
		sp.Close()
		return err
	}

	return nil
}

// ReadData wakes the MEMS 3 ECU and runs its request/response data loop.
//
// MEMS 3 speaks a KWP-style protocol at 9600 8E1 (note: even parity, unlike the
// other variants). It does not need a break-pulse wake-up: sending initCommand
// (1A 9A) is enough, and the ECU replies 5A 9A. Every frame is wrapped in a
// 3-byte address header (B8 13 F7), a length byte, the payload, then an XOR
// checksum (see sendCommand), so this loop reads buffer[3] to learn the payload
// length and waits for the whole frame before acting.
//
// The connect sequence after the init reply is: startDiagnostic (10 A0) ->
// requestSeed (27 01) -> sendKey (27 02 + key) -> ping (3E), where the key is
// derived from the seed by ecu.GenerateKey (a seed of 0 means no auth needed).
// Once authenticated it polls faults then the data PIDs and loops on ping.
// Frames whose header is our own request echo are skipped.
func (m *MEMS3) ReadData(ctx context.Context) error {
	m.sendCommand(initCommand)

	buffer := make([]byte, 0)
	readLoops := 0
	readLoopsLimit := 200

	// Reused across iterations; contents are copied into buffer immediately.
	rb := make([]byte, 128)

	for readLoops < readLoopsLimit {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		readLoops++
		if readLoops > 1 {
			time.Sleep(10 * time.Millisecond)
		}

		n, err := m.sp.Read(rb)
		if err != nil {
			var te interface{ Timeout() bool }
			if !errors.As(err, &te) || !te.Timeout() {
				return fmt.Errorf("serial read: %w", err)
			}
		}
		buffer = append(buffer, rb[:n]...)
		if n > 0 {
			readLoops = 0
		}

		if len(buffer) == 0 {
			continue
		}

		if len(buffer) >= 2 && bytes.Equal(buffer[0:2], initCommand) {
			m.state.LogDebug("Got our init echo")
			buffer = buffer[2:]
			continue
		}

		if len(buffer) < 4 {
			continue
		}
		dataLength := int(buffer[3])
		totalLength := 3 + 1 + dataLength + 1
		if len(buffer) < totalLength {
			continue
		}

		actualData := buffer[4 : 4+dataLength]

		// A frame carrying our own address header is the echo of a request we
		// sent; the ECU's replies do not repeat it.
		if bytes.Equal(buffer[0:3], requestHeader) {
			buffer = buffer[totalLength:]
			continue
		}

		reply, handled := m.handleFrame(actualData)
		if !handled {
			m.state.LogDebugf("unknown command in buffer (burning it): got %d bytes \n%s", len(buffer), hex.Dump(buffer[0:totalLength]))
			m.state.LogDebugf("actualData %d bytes \n%s", len(actualData), hex.Dump(actualData))
			buffer = buffer[totalLength:]
			continue
		}

		buffer = nil
		time.Sleep(50 * time.Millisecond)
		m.sendNextCommand(reply)
	}

	if readLoops >= readLoopsLimit {
		m.state.LogDebugf("had buffer data: got %d bytes \n%s", len(buffer), hex.Dump(buffer))
		return errors.New("MEMS 3 timed out")
	}
	m.state.LogDebug("fell out of readloop")
	return nil
}

// handleFrame interprets one decoded MEMS 3 payload, updating the shared state.
//
// It returns the canonical reply constant to feed to sendNextCommand, and
// whether the frame was recognised at all; an unrecognised frame is left for the
// caller to log and discard. The returned constant is deliberately not
// actualData: the state machine keys off the fixed response codes, not off the
// variable-length payload that carried them.
//
// The State lock is taken only around the writes, never across the caller's
// inter-frame delay.
func (m *MEMS3) handleFrame(actualData []byte) (reply []byte, handled bool) {
	switch {
	case len(actualData) >= 2 && bytes.Equal(actualData[0:2], initAccepted):
		m.state.Lock()
		m.state.Connected = true
		m.state.Unlock()
		m.state.LogDebug("< ECU woke up")
		return initAccepted, true

	case bytes.Equal(actualData, startDiagResponse):
		m.state.LogDebug("< Diag mode accepted")
		return startDiagResponse, true

	case len(actualData) >= 4 && bytes.Equal(actualData[0:2], seedResponse):
		m.seed = int(actualData[2])<<8 + int(actualData[3])
		m.state.LogDebugf("< seed %d", m.seed)
		if m.seed == 0 {
			// Already unlocked: no key to send, so fall through to the ping that
			// sendNextCommand issues for an unrecognised reply.
			m.key = 0
			m.state.LogDebug("Auth not required, collecting data...")
			return nil, true
		}
		m.key = ecu.GenerateKey(m.seed)
		return seedResponse, true

	case bytes.Equal(actualData, keyAcceptResponse):
		m.state.LogDebug("< Key accepted, collecting data...")
		return keyAcceptResponse, true

	case bytes.Equal(actualData, pongResponse):
		m.state.LogDebug(".")
		return pongResponse, true

	case bytes.Equal(actualData, faultsClearedResponse):
		m.state.SetAlert("ECU reports faults cleared")
		m.state.LogDebug("< FAULTS CLEARED")
		return faultsClearedResponse, true

	case len(actualData) >= len(responseFaults) && bytes.Equal(actualData[0:len(responseFaults)], responseFaults):
		m.parseFaults(actualData)
		return responseFaults, true
	}

	if len(actualData) < 2 {
		return nil, false
	}

	// Data PIDs: each is a fixed set of 16-bit big-endian fields.
	m.state.Lock()
	defer m.state.Unlock()

	switch {
	case bytes.Equal(actualData[0:2], responseData00):
		if len(actualData) < 12 {
			return nil, false
		}
		m.state.Data["coolant_temp"] = float32(int(actualData[2])<<8+int(actualData[3])-2730) / 10
		m.state.Data["oil_temp"] = float32(int(actualData[6])<<8+int(actualData[7])-2730) / 10
		m.state.Data["intake_air_temp"] = float32(int(actualData[10])<<8+int(actualData[11])-2730) / 10
		return responseData00, true

	case bytes.Equal(actualData[0:2], responseData06):
		if len(actualData) < 12 {
			return nil, false
		}
		m.state.Data["map_sensor_kpa"] = float32(int(actualData[2])<<8+int(actualData[3])) / 100
		m.state.Data["throttle_mv"] = float32(int(actualData[8])<<8 + int(actualData[9]))
		m.state.Data["rpm"] = float32(int(actualData[10])<<8 + int(actualData[11]))
		return responseData06, true

	case bytes.Equal(actualData[0:2], responseData0A):
		if len(actualData) < 6 {
			return nil, false
		}
		m.state.Data["fuel_feedback_percent"] = float32(int(actualData[2])<<8+int(actualData[3])) / 100
		m.state.Data["lambda_mv"] = float32(int(actualData[4])<<8 + int(actualData[5]))
		return responseData0A, true

	case bytes.Equal(actualData[0:2], responseData0B):
		if len(actualData) < 6 {
			return nil, false
		}
		m.state.Data["coil_1_time_uS"] = float32(int(actualData[2])<<8 + int(actualData[3]))
		m.state.Data["coil_2_time_uS"] = float32(int(actualData[4])<<8 + int(actualData[5]))
		return responseData0B, true

	case bytes.Equal(actualData[0:2], responseData21):
		if len(actualData) < 4 {
			return nil, false
		}
		m.state.Data["rpm_deviation"] = float32(int(actualData[2])<<8 + int(actualData[3]))
		return responseData21, true
	}

	return nil, false
}

// takeUserCommand consumes any pending user command, returning the bytes to
// send. ok is false when nothing is pending or the name is unknown; either way
// the pending command is cleared so an unrecognised name cannot be retried on
// every iteration for the life of the session.
func (m *MEMS3) takeUserCommand() (command []byte, ok bool) {
	m.state.Lock()
	name := m.state.UserCommand
	m.state.UserCommand = ""
	m.state.Unlock()

	if name == "" {
		return nil, false
	}
	command, ok = userCommands[name]
	if !ok {
		m.state.LogDebug("Asked to perform a user command but don't understand it")
		return nil, false
	}
	return command, true
}

// sendNextCommand picks the next MEMS 3 request from the previous reply, walking
// the init/auth/poll sequence documented on ReadData (init -> diag -> seed/key ->
// ping -> faults -> data PIDs -> ping). A pending user command pre-empts it.
func (m *MEMS3) sendNextCommand(previousResponse []byte) {
	if command, ok := m.takeUserCommand(); ok {
		m.sendCommand(command)
		return
	}

	if bytes.Equal(previousResponse, seedResponse) {
		// Build on a fresh slice: appending to the package-level sendKey would
		// write through to it the moment that literal gained spare capacity.
		command := make([]byte, 0, len(sendKey)+2)
		command = append(command, sendKey...)
		command = append(command, byte(m.key>>8), byte(m.key&0xFF))
		m.sendCommand(command)
		return
	}

	if next, ok := nextAfterResponse[string(previousResponse)]; ok {
		m.sendCommand(next)
		return
	}

	m.sendCommand(pingCommand)
}

// sendCommand frames and writes a MEMS 3 command.
//
// Frame = [B8 13 F7 (address header)][len][payload...][XOR checksum], where the
// checksum is the XOR of every preceding byte including the header and length.
// The header identifies the diagnostic tool/ECU pair; the ECU prefixes its own
// replies with the same header, which is how the read loop tells a response apart
// from a request echo.
func (m *MEMS3) sendCommand(data []byte) {
	// Copy the header rather than appending to it: `output := requestHeader`
	// followed by append writes through to the package-level slice as soon as it
	// has spare capacity.
	output := make([]byte, 0, len(requestHeader)+2+len(data))
	output = append(output, requestHeader...)
	output = append(output, byte(len(data)))
	output = append(output, data...)
	output = append(output, xorAllBytes(output))
	if _, err := m.sp.Write(output); err != nil {
		m.state.LogDebugf("serial write failed: %v", err)
	}
}

// parseFaults decodes the MEMS 3 fault response (0x58 ...).
//
// After the 0x58 response code, faults are a flat list of 3-byte records:
// 2 bytes of fault number (big-endian) plus 1 byte of fault status. The number
// is looked up in the faults table for a human label and the status byte in
// faultTypes (present / historic / test-not-complete); unknown values are kept
// as raw numbers so nothing is silently dropped.
func (m *MEMS3) parseFaults(buffer []byte) {
	faultList := []string{}
	buffer = buffer[2:]
	for len(buffer) >= 3 {
		thisFault := int(buffer[0])<<8 + int(buffer[1])
		faultType := int(buffer[2])

		outputFaultType, ok := faultTypes[faultType]
		if !ok {
			outputFaultType = "unknown (" + strconv.Itoa(faultType) + ")"
		}

		outputFault, ok := faults[thisFault]
		if !ok {
			outputFault = "unknown (" + strconv.Itoa(thisFault) + ")"
		}

		fullOutputText := "Fault - " + outputFault + " - " + outputFaultType
		faultList = append(faultList, fullOutputText)

		if len(buffer) > 3 {
			buffer = buffer[3:]
		} else {
			buffer = nil
		}
	}
	m.state.Faults = faultList
}

func (m *MEMS3) Close() error {
	m.state.Lock()
	m.state.Connected = false
	m.state.Unlock()
	if m.sp != nil {
		return m.sp.Close()
	}
	return nil
}

func (m *MEMS3) Type() string {
	return "3"
}

func xorAllBytes(data []byte) byte {
	result := byte(0)
	for _, b := range data {
		result ^= b
	}
	return result
}
