package mems1x

import (
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/distributed/sers"
)

var (
	gotKlineEcho  = false
	lastKlineByte = byte(0x00)

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
)

func (m *MEMS1x) nextCommand(previousResponse byte) byte {
	m.state.Lock()
	cmd := m.state.UserCommand
	m.state.Unlock()

	if cmd != "" {
		command, ok := userCommands[cmd]
		if ok {
			m.state.Lock()
			m.state.UserCommand = ""
			m.state.Unlock()
			fmt.Println("> " + cmd)
			return command
		} else {
			m.state.LogDebug("Unknown user command:", cmd)
			m.state.Lock()
			m.state.UserCommand = ""
			m.state.Unlock()
		}
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

func (m *MEMS1x) send(sp sers.SerialPort, data byte) {
	m.state.LogDebugf("Sending byte: %02X", data)
	sp.Write([]byte{data})
	gotKlineEcho = false
	lastKlineByte = data
}

func (m *MEMS1x) loop(kline bool) ([]byte, error) {
	sp := m.sp
	m.send(sp, 0xCA)

	buffer := make([]byte, 0)
	readLoops := 0
	readLoopsLimit := 200

READLOOP:
	for readLoops < readLoopsLimit {
		readLoops++
		if readLoops > 1 {
			time.Sleep(10 * time.Millisecond)
		}

		rb := make([]byte, 128)
		n, _ := sp.Read(rb[:])
		rb = rb[0:n]
		buffer = append(buffer, rb...)
		if n > 0 {
			readLoops = 0
		}

		if len(buffer) == 0 {
			continue
		}

		if kline && !gotKlineEcho {
			if buffer[0] == lastKlineByte {
				gotKlineEcho = true
				m.state.LogDebugf("K-line echo consumed: %02X (buffer remaining: %v)", buffer[0], buffer[1:])
				buffer = buffer[1:]
				continue
			} else {
				m.state.LogDebugf("Expected K-line echo %02X, got %02X", lastKlineByte, buffer[0])
			}
		}

		if len(buffer) == 0 {
			continue
		}

		if len(buffer) >= 2 {
			for key := range userCommands {
				if buffer[0] == userCommands[key] {
					fmt.Println("< " + key)
					m.state.Lock()
					m.state.Alert = "ECU accepted " + key
					m.state.Unlock()
					m.send(sp, m.nextCommand(buffer[0]))
					buffer = nil
					continue READLOOP
				}
			}
		}

		m.state.LogDebugf("Processing byte: %02X", buffer[0])
		switch buffer[0] {
		case requestClearFaults:
			if len(buffer) >= 2 && buffer[1] == 0x00 {
				fmt.Println("< FAULTS CLEARED")
				m.state.Lock()
				m.state.Alert = "ECU reports faults cleared"
				m.state.Unlock()
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
		m.state.LogDebugf("Timed out — buffer: %d bytes\n%s", len(buffer), hex.Dump(buffer))
		return nil, errors.New("MEMS 1.x timed out")
	}
	m.state.LogDebug("Read loop exited normally")

	return nil, nil
}

func (m *MEMS1x) parseData80(data []byte) {
	m.state.Lock()
	defer m.state.Unlock()

	faults := []string{}
	m.state.LogDebugf("data 0x80: %d bytes\n%s", len(data), hex.Dump(data))

	data = data[1:]
	packetSize := int(data[0])

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
	m.state.Data["throttle_pot_voltage"] = float32(data[9]) / 200
	m.state.Data["idle_switch"] = float32((int(data[10]) & 0x00001000) >> 3)
	m.state.Data["park_or_neutral_switch"] = float32(data[12])

	if ((int(data[13]) >> 0) & 1) > 0 {
		faults = append(faults, "fault_coolant_temp_sensor")
	}
	if ((int(data[13]) >> 1) & 1) > 0 {
		faults = append(faults, "fault_inlet_air_temp_sensor")
	}
	if ((int(data[13]) >> 3) & 1) > 0 {
		faults = append(faults, "fault_turbo_overboost")
	}
	if ((int(data[13]) >> 4) & 1) > 0 {
		faults = append(faults, "fault_ambient_temp_sensor")
	}
	if ((int(data[13]) >> 5) & 1) > 0 {
		faults = append(faults, "fault_fuel_rail_temp_sensor")
	}
	if ((int(data[13]) >> 6) & 1) > 0 {
		faults = append(faults, "fault_knock_detected")
	}

	if ((int(data[14]) >> 0) & 1) > 0 {
		faults = append(faults, "fault_coolant_temp_gauge")
	}
	if ((int(data[14]) >> 1) & 1) > 0 {
		faults = append(faults, "fault_fuel_pump_circuit")
	}
	if ((int(data[14]) >> 3) & 1) > 0 {
		faults = append(faults, "fault_air_con_clutch")
	}
	if ((int(data[14]) >> 4) & 1) > 0 {
		faults = append(faults, "fault_purge_valve")
	}
	if ((int(data[14]) >> 5) & 1) > 0 {
		faults = append(faults, "fault_map_sensor")
	}
	if ((int(data[14]) >> 6) & 1) > 0 {
		faults = append(faults, "fault_boost_valve")
	}
	if ((int(data[14]) >> 7) & 1) > 0 {
		faults = append(faults, "fault_throttle_pot_circuit")
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
		m.state.Data["ignition_advance"] = float32(data[0x16] / 2)
	}
	if packetSize > 0x18 {
		coilTime := int(data[0x17]) << 8
		coilTime += int(data[0x18])
		m.state.Data["coil_time_microseconds"] = float32(coilTime) * 2
	}

	m.state.Faults = faults
}

func (m *MEMS1x) parseData7D(data []byte) {
	m.state.Lock()
	defer m.state.Unlock()
	m.state.LogDebug("ECU 1.x data 7D: " + hex.Dump(data))

	data = data[1:]
	packetSize := int(data[0])

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
