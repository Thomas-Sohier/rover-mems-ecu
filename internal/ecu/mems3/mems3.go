package mems3

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"rover-mems-agent/internal/ecu"

	"github.com/distributed/sers"
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
	sp    sers.SerialPort
	seed  int
	key   int
}

// NewMEMS3 creates a new MEMS 3 ECU handler.
func NewMEMS3(state *ecu.State, cfg ecu.Config) (ecu.ECU, error) {
	state.DebugMode = cfg.DebugMode
	return &MEMS3{state: state}, nil
}

func (m *MEMS3) Connect(portName string) error {
	fmt.Println("Connecting to MEMS 3 ECU")
	m.state.Lock()
	m.state.Connected = false
	m.state.Unlock()

	sp, err := sers.Open(portName)
	if err != nil {
		return err
	}
	m.sp = sp

	err = sp.SetMode(9600, 8, sers.E, 1, sers.NO_HANDSHAKE)
	if err != nil {
		sp.Close()
		return err
	}

	err = sp.SetReadParams(0, 0.001)
	if err != nil {
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
func (m *MEMS3) ReadData() error {
	m.sendCommand(initCommand)

	buffer := make([]byte, 0)
	readLoops := 0
	readLoopsLimit := 200

	for readLoops < readLoopsLimit {
		readLoops++
		if readLoops > 1 {
			time.Sleep(10 * time.Millisecond)
		}

		rb := make([]byte, 128)
		n, _ := m.sp.Read(rb[:])
		rb = rb[0:n]
		buffer = append(buffer, rb...)
		if n > 0 {
			readLoops = 0
		}

		if len(buffer) == 0 {
			continue
		}

		if len(buffer) >= 2 && slicesEqual(buffer[0:2], initCommand) {
			fmt.Println("Got our init echo")
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

		if slicesEqual(buffer[0:3], requestHeader) {
			if slicesEqual(actualData, initCommand) ||
				slicesEqual(actualData, startDiagnostic) ||
				slicesEqual(actualData, requestSeed) ||
				(len(actualData) >= 2 && slicesEqual(actualData[0:2], sendKey)) ||
				slicesEqual(actualData, pingCommand) {
				buffer = buffer[totalLength:]
				continue
			}
			buffer = buffer[totalLength:]
			continue
		}

		m.state.Lock()

		if len(actualData) >= 2 && slicesEqual(actualData[0:2], initAccepted) {
			fmt.Println("< ECU woke up")
			buffer = nil
			m.state.Connected = true
			m.state.Unlock()
			time.Sleep(50 * time.Millisecond)
			m.sendNextCommand(initAccepted)
			continue
		}
		if slicesEqual(actualData, startDiagResponse) {
			fmt.Println("< Diag mode accepted")
			buffer = nil
			m.state.Unlock()
			time.Sleep(50 * time.Millisecond)
			m.sendNextCommand(startDiagResponse)
			continue
		}
		if len(actualData) >= 2 && slicesEqual(actualData[0:2], seedResponse) {
			fmt.Println("< seed ")
			m.seed = int(actualData[2]) << 8
			m.seed += int(actualData[3])
			fmt.Println(m.seed)
			if m.seed == 0 {
				m.key = 0
				buffer = nil
				m.state.Unlock()
				time.Sleep(50 * time.Millisecond)
				m.sendNextCommand(nil)
				fmt.Println("Auth not required, collecting data...")
				continue
			} else {
				m.key = ecu.GenerateKey(m.seed)
				buffer = nil
				m.state.Unlock()
				time.Sleep(50 * time.Millisecond)
				m.sendNextCommand(seedResponse)
				continue
			}
		}
		if slicesEqual(actualData, keyAcceptResponse) {
			fmt.Println("< Key accepted, collecting data...")
			buffer = nil
			m.state.Unlock()
			time.Sleep(50 * time.Millisecond)
			m.sendNextCommand(keyAcceptResponse)
			continue
		}
		if slicesEqual(actualData, pongResponse) {
			fmt.Print(".")
			buffer = nil
			m.state.Unlock()
			time.Sleep(50 * time.Millisecond)
			m.sendNextCommand(pongResponse)
			continue
		}
		if slicesEqual(actualData, faultsClearedResponse) {
			fmt.Println("< FAULTS CLEARED")
			m.state.Alert = "ECU reports faults cleared"
			buffer = nil
			m.state.Unlock()
			time.Sleep(50 * time.Millisecond)
			m.sendNextCommand(faultsClearedResponse)
			continue
		}

		if len(actualData) >= len(responseFaults) && slicesEqual(actualData[0:len(responseFaults)], responseFaults) {
			m.parseFaults(actualData)
			buffer = nil
			m.state.Unlock()
			time.Sleep(50 * time.Millisecond)
			m.sendNextCommand(responseFaults)
			continue
		}

		if len(actualData) >= 2 && slicesEqual(actualData[0:2], responseData00) {
			coolantTemp := int(actualData[2])<<8 + int(actualData[3]) - 2730
			m.state.Data["coolant_temp"] = float32(coolantTemp) / 10
			oilTemp := int(actualData[6])<<8 + int(actualData[7]) - 2730
			m.state.Data["oil_temp"] = float32(oilTemp) / 10
			intakeAirTemp := int(actualData[10])<<8 + int(actualData[11]) - 2730
			m.state.Data["intake_air_temp"] = float32(intakeAirTemp) / 10
			buffer = nil
			m.state.Unlock()
			time.Sleep(50 * time.Millisecond)
			m.sendNextCommand(responseData00)
			continue
		}
		if len(actualData) >= 2 && slicesEqual(actualData[0:2], responseData06) {
			mapKpa := int(actualData[2])<<8 + int(actualData[3])
			m.state.Data["map_sensor_kpa"] = float32(mapKpa) / 100
			throttleMv := int(actualData[8])<<8 + int(actualData[9])
			m.state.Data["throttle_mv"] = float32(throttleMv)
			rpm := int(actualData[10])<<8 + int(actualData[11])
			m.state.Data["rpm"] = float32(rpm)
			buffer = nil
			m.state.Unlock()
			time.Sleep(50 * time.Millisecond)
			m.sendNextCommand(responseData06)
			continue
		}
		if len(actualData) >= 2 && slicesEqual(actualData[0:2], responseData0A) {
			fuelFeedback := int(actualData[2])<<8 + int(actualData[3])
			m.state.Data["fuel_feedback_percent"] = float32(fuelFeedback) / 100
			preLambdaMv := int(actualData[4])<<8 + int(actualData[5])
			m.state.Data["lambda_mv"] = float32(preLambdaMv)
			buffer = nil
			m.state.Unlock()
			time.Sleep(50 * time.Millisecond)
			m.sendNextCommand(responseData0A)
			continue
		}
		if len(actualData) >= 2 && slicesEqual(actualData[0:2], responseData0B) {
			coil1 := int(actualData[2])<<8 + int(actualData[3])
			m.state.Data["coil_1_time_uS"] = float32(coil1)
			coil2 := int(actualData[4])<<8 + int(actualData[5])
			m.state.Data["coil_2_time_uS"] = float32(coil2)
			buffer = nil
			m.state.Unlock()
			time.Sleep(50 * time.Millisecond)
			m.sendNextCommand(responseData0B)
			continue
		}
		if len(actualData) >= 2 && slicesEqual(actualData[0:2], responseData21) {
			rpmdev := int(actualData[2])<<8 + int(actualData[3])
			m.state.Data["rpm_deviation"] = float32(rpmdev)
			buffer = nil
			m.state.Unlock()
			time.Sleep(50 * time.Millisecond)
			m.sendNextCommand(responseData21)
			continue
		}

		fmt.Printf("unknown command in buffer (burning it): got %d bytes \n%s", len(buffer), hex.Dump(buffer[0:totalLength]))
		fmt.Printf("actualData %d bytes \n%s", len(actualData), hex.Dump(actualData))
		buffer = buffer[totalLength:]
		m.state.Unlock()
	}

	if readLoops >= readLoopsLimit {
		fmt.Printf("had buffer data: got %d bytes \n%s", len(buffer), hex.Dump(buffer))
		return errors.New("MEMS 3 timed out")
	}
	fmt.Println("fell out of readloop")
	return nil
}

// sendNextCommand picks the next MEMS 3 request from the previous reply, walking
// the init/auth/poll sequence documented on ReadData (init -> diag -> seed/key ->
// ping -> faults -> data PIDs -> ping). A pending user command pre-empts it.
func (m *MEMS3) sendNextCommand(previousResponse []byte) {
	m.state.Lock()
	cmd := m.state.UserCommand
	m.state.Unlock()

	if cmd != "" {
		command, ok := userCommands[cmd]
		if ok {
			m.state.Lock()
			m.state.UserCommand = ""
			m.state.Unlock()
			m.sendCommand(command)
			return
		} else {
			fmt.Println("Asked to perform a user command but don't understand it")
		}
	}

	if slicesEqual(previousResponse, initAccepted) {
		m.sendCommand(startDiagnostic)
	} else if slicesEqual(previousResponse, startDiagResponse) {
		m.sendCommand(requestSeed)
	} else if slicesEqual(previousResponse, seedResponse) {
		command := append(sendKey, byte(m.key>>8))
		command = append(command, byte(m.key&0xFF))
		m.sendCommand(command)
	} else if slicesEqual(previousResponse, keyAcceptResponse) {
		m.sendCommand(pingCommand)
	} else if slicesEqual(previousResponse, pongResponse) {
		m.sendCommand(requestFaultsCommand)
	} else if slicesEqual(previousResponse, responseFaults) {
		m.sendCommand(requestData00)
	} else if slicesEqual(previousResponse, responseData00) {
		m.sendCommand(requestData06)
	} else if slicesEqual(previousResponse, responseData06) {
		m.sendCommand(requestData0A)
	} else if slicesEqual(previousResponse, responseData0A) {
		m.sendCommand(requestData0B)
	} else if slicesEqual(previousResponse, responseData0B) {
		m.sendCommand(requestData21)
	} else if slicesEqual(previousResponse, responseData21) {
		m.sendCommand(pingCommand)
	} else if slicesEqual(previousResponse, faultsClearedResponse) {
		m.sendCommand(requestFaultsCommand)
	} else {
		m.sendCommand(pingCommand)
	}
}

// sendCommand frames and writes a MEMS 3 command.
//
// Frame = [B8 13 F7 (address header)][len][payload...][XOR checksum], where the
// checksum is the XOR of every preceding byte including the header and length.
// The header identifies the diagnostic tool/ECU pair; the ECU prefixes its own
// replies with the same header, which is how the read loop tells a response apart
// from a request echo.
func (m *MEMS3) sendCommand(data []byte) {
	output := requestHeader
	output = append(output, byte(len(data)))
	output = append(output, data...)
	output = append(output, xorAllBytes(output))
	m.sp.Write(output)
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

func slicesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func xorAllBytes(data []byte) byte {
	result := byte(0)
	for _, b := range data {
		result ^= b
	}
	return result
}
