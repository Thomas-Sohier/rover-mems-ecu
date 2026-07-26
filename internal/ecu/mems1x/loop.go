package mems1x

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"rover-mems-agent/internal/serial"
)

var (
	requestClearFaults            = byte(0xCC)
	startTestRpmGauge             = byte(0x6B)
	startTestLambdaHeater         = byte(0x19)
	stopTestLambdaHeater          = byte(0x09)
	startTestACClutch             = byte(0x13)
	stopTestACClutch              = byte(0x03)
	startTestFuelPump             = byte(0x11)
	stopTestFuelPump              = byte(0x01)
	startTestFan1                 = byte(0x1D)
	stopTestFan1                  = byte(0x0D)
	startTestPurgeValve           = byte(0x18)
	stopTestPurgeValve            = byte(0x08)
	increaseIdleDecay             = byte(0x89)
	decreaseIdleDecay             = byte(0x8A)
	increaseIdleSpeed             = byte(0x91)
	decreaseIdleSpeed             = byte(0x92)
	increaseIgnitionAdvanceOffset = byte(0x93)
	decreaseIgnitionAdvanceOffset = byte(0x94)
	increaseFuelTrim1             = byte(0x79)
	decreaseFuelTrim1             = byte(0x7A)
	increaseFuelTrim2             = byte(0x7B)
	decreaseFuelTrim2             = byte(0x7C)

	userCommands = map[string]byte{
		"clearfaults":                   requestClearFaults,
		"startTestRpmGauge":             startTestRpmGauge,
		"startTestLambdaHeater":         startTestLambdaHeater,
		"stopTestLambdaHeater":          stopTestLambdaHeater,
		"startTestACClutch":             startTestACClutch,
		"stopTestACClutch":              stopTestACClutch,
		"startTestFuelPump":             startTestFuelPump,
		"stopTestFuelPump":              stopTestFuelPump,
		"startTestFan1":                 startTestFan1,
		"stopTestFan1":                  stopTestFan1,
		"startTestPurgeValve":           startTestPurgeValve,
		"stopTestPurgeValve":            stopTestPurgeValve,
		"increaseIdleDecay":             increaseIdleDecay,
		"decreaseIdleDecay":             decreaseIdleDecay,
		"increaseIdleSpeed":             increaseIdleSpeed,
		"decreaseIdleSpeed":             decreaseIdleSpeed,
		"increaseIgnitionAdvanceOffset": increaseIgnitionAdvanceOffset,
		"decreaseIgnitionAdvanceOffset": decreaseIgnitionAdvanceOffset,
		"increaseFuelTrim1":             increaseFuelTrim1,
		"decreaseFuelTrim1":             decreaseFuelTrim1,
		"increaseFuelTrim2":             increaseFuelTrim2,
		"decreaseFuelTrim2":             decreaseFuelTrim2,
	}

	// commandNames is the reverse of userCommands, so the read loop can name an
	// acknowledged command with one lookup instead of scanning all 22 entries
	// for every byte it processes.
	commandNames = func() map[byte]string {
		m := make(map[byte]string, len(userCommands))
		for name, b := range userCommands {
			m[b] = name
		}
		return m
	}()
)

// nextCommand decides which byte to send next, given the byte the ECU just
// answered with. The MEMS 1.x protocol is strictly request/response: we send one
// command byte, the ECU replies, and that reply tells us what to send next.
//
// A pending user command (clear faults, actuator test, etc.) always takes
// priority. Otherwise we walk the fixed init/poll state machine described by the
// rovermems technical page (https://rovermems.com/diagnostics/technical/):
//
//	CA -> 75 -> F4 -> D0 -> 80 -> 7D -> 80 -> 7D ...
//
// CA/75/D0 is the documented hand-shake (D0 returns the 4-byte ECU ID); F4 is the
// extra "select diagnostic mode" step the real ECU expects before it will stream
// data; 0x80 and 0x7D are the two live data frames, which we then alternate
// between forever. After clearing faults (0xCC) we jump straight back to 0x80.
func (m *MEMS1x) nextCommand(previousResponse byte) byte {
	m.state.Lock()
	cmd := m.state.UserCommand
	m.state.UserCommand = ""
	m.state.Unlock()

	if cmd != "" {
		if command, ok := userCommands[cmd]; ok {
			m.state.LogDebug("> " + cmd)
			return command
		}
		m.state.LogDebug("Unknown user command:", cmd)
	}

	switch previousResponse {
	case requestClearFaults:
		return 0x80
	case 0xCA:
		return 0x75
	case 0x75:
		return 0xF4
	case 0xF4:
		return 0xD0
	case 0xD0:
		return 0x80
	case 0x80:
		return 0x7D
	case 0x7D:
		return 0x80
	}

	return 0x80
}

// send writes a single command byte and arms the K-line echo tracking.
//
// MEMS 1.x is a single-wire half-duplex K-line: every byte we transmit is
// electrically echoed straight back to us. We record the byte we just sent
// (lastKlineByte) and clear gotKlineEcho so the read loop knows to discard that
// echo before treating anything as a genuine ECU reply.
func (m *MEMS1x) send(sp serial.Port, data byte) {
	m.state.LogDebugf("Sending byte: %02X", data)
	if _, err := sp.Write([]byte{data}); err != nil {
		m.state.LogDebugf("serial write failed: %v", err)
	}
	m.gotKlineEcho = false
	m.lastKlineByte = data
}

// loop runs the MEMS 1.x request/response data loop until it errors or times out.
//
// It kicks the conversation off with 0xCA (the first byte of the documented
// CA/75/D0 handshake) and then, for each byte received, consumes our own K-line
// echo (when kline is true), recognises the reply, parses any data frame, and
// sends the next command via nextCommand.
//
// Frame length handling follows the rovermems technical page: byte 0 of a data
// frame is the command echo (0x80 / 0x7D) and byte 1 is the packet size, so the
// full frame is buffer[1]+1 bytes — we wait until that many bytes have arrived
// before parsing. readLoops counts consecutive empty reads and aborts after the
// limit so a dead/unplugged ECU surfaces as a timeout instead of hanging.
func (m *MEMS1x) loop(ctx context.Context, kline bool) error {
	sp := m.sp
	m.send(sp, 0xCA)

	buffer := make([]byte, 0)
	readLoops := 0
	readLoopsLimit := 200

	// Reused across iterations: the bytes are copied into buffer immediately, so
	// nothing outlives the loop body. Allocating per iteration churned ~13 KB/s
	// at the loop's 10 ms cadence, forever, on an embedded target.
	rb := make([]byte, 128)

READLOOP:
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

		n, err := sp.Read(rb)
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

		if kline && !m.gotKlineEcho {
			if buffer[0] == m.lastKlineByte {
				m.gotKlineEcho = true
				m.state.LogDebugf("K-line echo consumed: %02X (buffer remaining: %v)", buffer[0], buffer[1:])
				buffer = buffer[1:]
				continue
			} else {
				m.state.LogDebugf("Expected K-line echo %02X, got %02X", m.lastKlineByte, buffer[0])
			}
		}

		if len(buffer) == 0 {
			continue
		}

		if len(buffer) >= 2 {
			if name, ok := commandNames[buffer[0]]; ok {
				m.state.LogDebug("< " + name)
				m.state.SetAlert("ECU accepted " + name)
				m.send(sp, m.nextCommand(buffer[0]))
				buffer = nil
				continue READLOOP
			}
		}

		m.state.LogDebugf("Processing byte: %02X", buffer[0])
		switch buffer[0] {
		case requestClearFaults:
			if len(buffer) >= 2 && buffer[1] == 0x00 {
				m.state.LogDebug("< FAULTS CLEARED")
				m.state.SetAlert("ECU reports faults cleared")
				m.send(sp, m.nextCommand(buffer[0]))
				buffer = nil
				continue
			}
		case 0xCA:
			m.state.LogDebug("Got 0xCA")
			m.send(sp, m.nextCommand(buffer[0]))
			buffer = nil
			continue

		case 0x75:
			m.state.LogDebug("Got 0x75")
			m.send(sp, m.nextCommand(buffer[0]))
			buffer = nil
			continue

		case 0xF4:
			if len(buffer) >= 2 && buffer[1] == 0x00 {
				m.state.LogDebug("Got 0xF4 0x00")
				m.send(sp, m.nextCommand(buffer[0]))
				buffer = nil
				continue
			}
		case 0xD0:
			if len(buffer) >= 5 {
				m.state.Lock()
				m.state.Connected = true
				m.state.Unlock()
				m.state.LogDebugf("Got 0xD0, ECU ID: %s", hex.Dump(buffer[1:5]))
				m.send(sp, m.nextCommand(buffer[0]))
				buffer = nil
				continue
			}
		case 0x80:
			if len(buffer) >= 2 {
				fullLength := int(buffer[1]) + 1
				if len(buffer) >= fullLength {
					m.state.LogDebug("Got data 0x80")
					m.parseData80(buffer)
					m.send(sp, m.nextCommand(buffer[0]))
					buffer = nil
				}
			}
			continue

		case 0x7D:
			if len(buffer) >= 2 {
				fullLength := int(buffer[1]) + 1
				if len(buffer) >= fullLength {
					m.state.LogDebug("Got data 0x7D")
					m.parseData7D(buffer)
					m.send(sp, m.nextCommand(buffer[0]))
					buffer = nil
				}
			}
			continue
		default:
			m.state.LogDebugf("Unrecognised byte %02X — waiting for more data", buffer[0])
		}

	}
	if readLoops >= readLoopsLimit {
		// Name the command that went unanswered and say whether we ever saw its
		// K-line echo. Between them those two facts identify the failure without
		// a second run: no echo at all points at the adapter or the wiring,
		// whereas an echo with no reply points at the ECU or at that particular
		// command being wrong for this variant. A bare "timed out" identified
		// neither, which made bring-up on real hardware guesswork.
		echoState := "echo not seen"
		if !kline {
			echoState = "echo tracking off"
		} else if m.gotKlineEcho {
			echoState = "echo consumed"
		}
		m.state.LogDebugf("Timed out waiting for a reply to %02X (%s) — buffer: %d bytes\n%s",
			m.lastKlineByte, echoState, len(buffer), hex.Dump(buffer))
		return fmt.Errorf("MEMS 1.x timed out waiting for a reply to %02X (%s)", m.lastKlineByte, echoState)
	}
	m.state.LogDebug("Read loop exited normally")

	return nil
}

// faultBits80 is the DTC bitfield layout of the 0x80 frame: offsets 0x0D and
// 0x0E (after the command echo is dropped), one named fault per set bit. Listed
// as data so the layout can be read off against the protocol documentation
// rather than inferred from a column of near-identical if statements. Bit 2 of
// each byte and bit 7 of 0x0D are unassigned.
var faultBits80 = []struct {
	offset int
	bit    uint
	name   string
}{
	{0x0D, 0, "fault_coolant_temp_sensor"},
	{0x0D, 1, "fault_inlet_air_temp_sensor"},
	{0x0D, 3, "fault_turbo_overboost"},
	{0x0D, 4, "fault_ambient_temp_sensor"},
	{0x0D, 5, "fault_fuel_rail_temp_sensor"},
	{0x0D, 6, "fault_knock_detected"},

	{0x0E, 0, "fault_coolant_temp_gauge"},
	{0x0E, 1, "fault_fuel_pump_circuit"},
	{0x0E, 3, "fault_air_con_clutch"},
	{0x0E, 4, "fault_purge_valve"},
	{0x0E, 5, "fault_map_sensor"},
	{0x0E, 6, "fault_boost_valve"},
	{0x0E, 7, "fault_throttle_pot_circuit"},
}

// parseData80 decodes the 0x80 data frame.
//
// Layout and scaling come from the rovermems technical page
// (https://rovermems.com/diagnostics/technical/). After dropping the command
// echo (data[0] becomes the packet-size byte) each offset maps to a field, e.g.:
//
//	1-2  RPM            16-bit big-endian
//	3    coolant temp   degrees C, +55 offset (value-55)
//	4    ambient temp   value-55
//	5    intake air     value-55
//	7    MAP            kPa, direct
//	8    battery        0.1 V/LSB  (value/10)
//	9    throttle pot   0.02 V/LSB (value/50)
//	A    idle switch    bit 4 set = throttle closed
//	D,E  fault bitfields (see per-bit appends below)
//	F    idle setpoint  6.1 RPM/LSB
//	16   ign advance    0.5 deg/LSB with a -24 deg offset
//	17-18 coil dwell    2 us/LSB (0.002 ms)
//
// Trailing fields (idle setpoint onward) only exist on longer frames, so each is
// guarded by the packet size. The temperature "sentinel" checks (==59, ==200,
// ==35) flag the documented out-of-range values the ECU reports for a faulty
// sensor. Fields are written under the state lock because the web server reads
// the same map concurrently.
func (m *MEMS1x) parseData80(data []byte) {
	m.state.Lock()
	defer m.state.Unlock()

	faults := []string{}
	m.state.LogDebugf("data 0x80: %d bytes\n%s", len(data), hex.Dump(data))

	data = data[1:]
	packetSize := int(data[0])

	// The unconditional field accesses below reach up to data[14] (0xE).
	// A frame shorter than 15 bytes (after dropping the echo) is malformed.
	if len(data) < 15 {
		m.state.LogDebugf("data 0x80: short frame (packetSize=%d, got %d bytes) — skipping", packetSize, len(data))
		return
	}

	m.state.Data["rpm"] = float32((int(data[1]) << 8) + int(data[2]))
	m.state.Data["coolant_temp"] = float32(data[3]) - 55
	if data[3] == 59 {
		faults = append(faults, "fault_coolant_temp_value")
	}
	m.state.Data["ambient_temp"] = float32(data[4]) - 55
	if data[4] == 200 {
		faults = append(faults, "fault_ambient_temp_value")
	}
	m.state.Data["intake_air_temp"] = float32(data[5]) - 55
	if data[5] == 35 {
		faults = append(faults, "fault_intake_air_temp_value")
	}
	m.state.Data["fuel_rail_temp"] = float32(data[6]) - 55
	m.state.Data["map_sensor_kpa"] = float32(data[7])
	m.state.Data["battery_voltage"] = float32(data[8]) / 10
	m.state.Data["throttle_pot_voltage"] = float32(data[9]) / 50
	m.state.Data["idle_switch"] = float32((int(data[10]) & 0x10) >> 4)
	m.state.Data["park_or_neutral_switch"] = float32(data[12])

	for _, f := range faultBits80 {
		if data[f.offset]&(1<<f.bit) != 0 {
			faults = append(faults, f.name)
		}
	}

	if packetSize > 15 {
		m.state.Data["idle_setpoint"] = float32(data[15]) * 6.1
	}
	if packetSize > 16 {
		m.state.Data["idle_hotdb"] = float32(data[16])
	}
	if packetSize > 0x12 {
		m.state.Data["idle_valve_position"] = float32(data[0x12])
	}
	if packetSize > 0x14 {
		idleDeviation := int(data[0x13]) << 8
		idleDeviation += int(data[0x14])
		m.state.Data["idle_speed_deviation"] = float32(idleDeviation)
	}
	if packetSize > 0x15 {
		m.state.Data["ignition_advance_offset"] = float32(data[0x15])
	}
	if packetSize > 0x16 {
		m.state.Data["ignition_advance_raw"] = float32(data[0x16])
		m.state.Data["ignition_advance"] = float32(data[0x16])/2 - 24
	}
	if packetSize > 0x18 {
		coilTime := int(data[0x17]) << 8
		coilTime += int(data[0x18])
		m.state.Data["coil_time_microseconds"] = float32(coilTime) * 2
	}

	m.state.Faults = faults
}

// parseData7D decodes the 0x7D data frame, the second of the two live frames
// described on the rovermems technical page. As with 0x80 the command echo is
// dropped first so data[0] is the packet size, and the remaining offsets map to
// fields (throttle angle = value/2 degrees, lambda voltage, fuel trims, and
// several DTC bitfields). Offsets past the basic set are guarded by packet size
// because shorter frames omit them.
func (m *MEMS1x) parseData7D(data []byte) {
	m.state.Lock()
	defer m.state.Unlock()
	m.state.LogDebug("ECU 1.x data 7D: " + hex.Dump(data))

	data = data[1:]
	packetSize := int(data[0])

	// The unconditional field accesses below reach up to data[0xE] (14).
	// A frame shorter than 15 bytes (after dropping the echo) is malformed.
	if len(data) < 15 {
		m.state.LogDebugf("data 0x7D: short frame (packetSize=%d, got %d bytes) — skipping", packetSize, len(data))
		return
	}

	m.state.Data["ignition_switch"] = float32(data[1])
	m.state.Data["throttle_angle"] = float32(data[2]) / 2
	m.state.Data["air_fuel_ratio"] = float32(data[4])

	dtcByte := int(data[5])
	m.state.Data["lambda_heater_relay"] = float32((dtcByte >> 3) & 1)
	m.state.Data["secondary_trigger_sync"] = float32((dtcByte >> 4) & 1)
	m.state.Data["fan_1_control"] = float32((dtcByte >> 5) & 1)
	m.state.Data["fan_2_control"] = float32((dtcByte >> 7) & 1)
	m.state.Data["lambda_mv"] = float32(data[6]) * 5
	m.state.Data["lambda_sensor_frequency"] = float32(data[7])
	m.state.Data["lambda_sensor_duty_cycle"] = float32(data[8])
	m.state.Data["lambda_sensor_status"] = float32(data[9])
	if int(data[10]) > 0 {
		m.state.Data["closed_loop"] = 1
	} else {
		m.state.Data["closed_loop"] = 0
	}

	m.state.Data["long_term_trim"] = float32(data[11])
	m.state.Data["short_term_trim_percent"] = float32(data[12])
	m.state.Data["carbon_can_purge_valve_duty_cycle"] = float32(data[13])
	dtc2 := int(data[0xE])
	m.state.Data["primary_trigger_sync"] = float32((dtc2 >> 1) & 1)

	if packetSize >= 16 {
		m.state.Data["idle_base_position"] = float32(data[15])
	}
	if packetSize >= 21 {
		m.state.Data["idle_error"] = float32(data[20])
	}

	if packetSize >= 0x16 {
		dtc3 := int(data[0x16])
		m.state.Data["injector_1_4_driver"] = float32((dtc3 >> 1) & 1)
		m.state.Data["injector_2_3_driver"] = float32((dtc3 >> 2) & 1)
		m.state.Data["fault_engine_bay_vent_warning"] = float32((dtc3 >> 3) & 1)
		m.state.Data["engine_bay_vent_relay"] = float32((dtc3 >> 4) & 1)
		m.state.Data["hill_assist"] = float32((dtc3 >> 5) & 1)
		m.state.Data["cruise_control"] = float32((dtc3 >> 6) & 1)
	}
	if packetSize >= 0x1F {
		m.state.Data["crank_counter"] = float32(data[0x1F])
	}
}
