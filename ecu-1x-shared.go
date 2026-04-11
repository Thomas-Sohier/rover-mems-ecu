package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/distributed/sers"
)

var (
	ecu1xGotKlineEcho  = false
	ecu1xLastKlineByte = byte(0x00)

	ecu1xRequestClearFaults            = byte(0xCC)
	ecu1xStartTestRpmGauge             = byte(0x6B)
	ecu1xStartTestLambdaHeater         = byte(0x19)
	ecu1xStopTestLambdaHeater          = byte(0x09)
	ecu1xStartTestACClutch             = byte(0x13)
	ecu1xStopTestACClutch              = byte(0x03)
	ecu1xStartTestFuelPump             = byte(0x11)
	ecu1xStopTestFuelPump              = byte(0x01)
	ecu1xStartTestFan1                 = byte(0x1D)
	ecu1xStopTestFan1                  = byte(0x0D)
	ecu1xStartTestPurgeValve           = byte(0x18)
	ecu1xStopTestPurgeValve            = byte(0x08)
	ecu1xIncreaseIdleDecay             = byte(0x89)
	ecu1xDecreaseIdleDecay             = byte(0x8A)
	ecu1xIncreaseIdleSpeed             = byte(0x91)
	ecu1xDecreaseIdleSpeed             = byte(0x92)
	ecu1xIncreaseIgnitionAdvanceOffset = byte(0x93)
	ecu1xDecreaseIgnitionAdvanceOffset = byte(0x94)
	ecu1xIncreaseFuelTrim1             = byte(0x79)
	ecu1xDecreaseFuelTrim1             = byte(0x7A)
	ecu1xIncreaseFuelTrim2             = byte(0x7B)
	ecu1xDecreaseFuelTrim2             = byte(0x7C)

	ecu1xUserCommands = map[string]byte{
		"clearfaults":                   ecu1xRequestClearFaults,
		"startTestRpmGauge":             ecu1xStartTestRpmGauge,
		"startTestLambdaHeater":         ecu1xStartTestLambdaHeater,
		"stopTestLambdaHeater":          ecu1xStopTestLambdaHeater,
		"startTestACClutch":             ecu1xStartTestACClutch,
		"stopTestACClutch":              ecu1xStopTestACClutch,
		"startTestFuelPump":             ecu1xStartTestFuelPump,
		"stopTestFuelPump":              ecu1xStopTestFuelPump,
		"startTestFan1":                 ecu1xStartTestFan1,
		"stopTestFan1":                  ecu1xStopTestFan1,
		"startTestPurgeValve":           ecu1xStartTestPurgeValve,
		"stopTestPurgeValve":            ecu1xStopTestPurgeValve,
		"increaseIdleDecay":             ecu1xIncreaseIdleDecay,
		"decreaseIdleDecay":             ecu1xDecreaseIdleDecay,
		"increaseIdleSpeed":             ecu1xIncreaseIdleSpeed,
		"decreaseIdleSpeed":             ecu1xDecreaseIdleSpeed,
		"increaseIgnitionAdvanceOffset": ecu1xIncreaseIgnitionAdvanceOffset,
		"decreaseIgnitionAdvanceOffset": ecu1xDecreaseIgnitionAdvanceOffset,
		"increaseFuelTrim1":             ecu1xIncreaseFuelTrim1,
		"decreaseFuelTrim1":             ecu1xDecreaseFuelTrim1,
		"increaseFuelTrim2":             ecu1xIncreaseFuelTrim2,
		"decreaseFuelTrim2":             ecu1xDecreaseFuelTrim2,
	}
)

func ecu1xNextCommand(previousResponse byte) byte {
	if globalUserCommand != "" {
		command, ok := ecu1xUserCommands[globalUserCommand]
		if ok {
			// Capture before clearing so the log shows the actual command name
			cmd := globalUserCommand
			globalUserCommand = ""
			fmt.Println("> " + cmd)
			return command
		} else {
			logDebug("Unknown user command:", globalUserCommand)
			globalUserCommand = ""
		}
	}

	switch previousResponse {
	// go back to data 80 after clearing faults
	case ecu1xRequestClearFaults:
		return 0x80
	// init sequence then data 80
	case 0xCA:
		return 0x75
	case 0x75:
		return 0xF4
	case 0xF4:
		return 0xD0
	case 0xD0:
		return 0x80
	// toggle between data packets (1.2 ECU can only do 80 I think?)
	case 0x80:
		return 0x7D
	case 0x7D:
		return 0x80
	}

	return 0x80 // data 80 if we aren't sure
}

func ecu1xSend(sp sers.SerialPort, data byte) {
	logDebugf("Sending byte: %02X", data)
	sp.Write([]byte{data})
	ecu1xGotKlineEcho = false
	ecu1xLastKlineByte = data
}

func ecu1xLoop(sp sers.SerialPort, kline bool) ([]byte, error) {

	// start of init
	ecu1xSend(sp, 0xCA)

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
		rb = rb[0:n] // chop down to actual data size
		buffer = append(buffer, rb...)
		if n > 0 {
			readLoops = 0 // reset inactivity counter on any received byte
		}

		if len(buffer) == 0 {
			continue
		}

		if kline && !ecu1xGotKlineEcho {
			// K-line is half-duplex: the byte we send is echoed back on the same wire.
			// Discard our own echo before processing the ECU's actual response.
			if buffer[0] == ecu1xLastKlineByte {
				ecu1xGotKlineEcho = true
				logDebugf("K-line echo consumed: %02X (buffer remaining: %v)", buffer[0], buffer[1:])
				buffer = buffer[1:]
				continue
			} else {
				logDebugf("Expected K-line echo %02X, got %02X", ecu1xLastKlineByte, buffer[0])
			}
		}

		if len(buffer) == 0 {
			continue
		}

		// check through the user commands (if we got another byte back as well)
		if len(buffer) >= 2 {
			for key := range ecu1xUserCommands {
				if buffer[0] == ecu1xUserCommands[key] {
					fmt.Println("< " + key)
					globalAlert = "ECU accepted " + key
					ecu1xSend(sp, ecu1xNextCommand(buffer[0]))
					buffer = nil
					continue READLOOP // need to jump out twice
				}
			}
		}

		logDebugf("Processing byte: %02X", buffer[0])
		switch buffer[0] {
		case ecu1xRequestClearFaults:
			if len(buffer) >= 2 && buffer[1] == 0x00 {
				fmt.Println("< FAULTS CLEARED")
				globalAlert = "ECU reports faults cleared"
				ecu1xSend(sp, ecu1xNextCommand(buffer[0]))
				buffer = nil
				continue
			}
		case 0xCA:
			// Init step 1: ECU echoes 0xCA — reply with 0x75
			logDebug("Got 0xCA")
			ecu1xSend(sp, ecu1xNextCommand(buffer[0]))
			buffer = nil
			continue

		case 0x75:
			// Init step 2: ECU echoes 0x75 — reply with 0xF4
			logDebug("Got 0x75")
			ecu1xSend(sp, ecu1xNextCommand(buffer[0]))
			buffer = nil
			continue

		case 0xF4:
			// Init step 3: ECU echoes 0xF4 followed by 0x00 — reply with 0xD0
			if len(buffer) >= 2 && buffer[1] == 0x00 {
				logDebug("Got 0xF4 0x00")
				ecu1xSend(sp, ecu1xNextCommand(buffer[0]))
				buffer = nil
				continue
			}
		case 0xD0:
			// Init step 4: ECU echoes 0xD0 and sends 4-byte ECU ID — enter data mode
			if len(buffer) >= 5 {
				globalConnected = true
				logDebugf("Got 0xD0, ECU ID: %s", hex.Dump(buffer[1:5]))
				ecu1xSend(sp, ecu1xNextCommand(buffer[0]))
				buffer = nil
				continue
			}
		case 0x80:
			// Data packet type 1: engine data (RPM, temps, sensors…)
			if len(buffer) >= 2 {
				fullLength := int(buffer[1]) + 1
				if len(buffer) >= fullLength {
					logDebug("Got data 0x80")
					ecu1xParseData80(buffer)
					ecu1xSend(sp, ecu1xNextCommand(buffer[0]))
					buffer = nil
				}
			}
			// Accumulate until full packet arrives
			continue

		case 0x7D:
			// Data packet type 2: lambda, ignition, trim…
			if len(buffer) >= 2 {
				fullLength := int(buffer[1]) + 1
				if len(buffer) >= fullLength {
					logDebug("Got data 0x7D")
					ecu1xParseData7D(buffer)
					ecu1xSend(sp, ecu1xNextCommand(buffer[0]))
					buffer = nil
				}
			}
			// Accumulate until full packet arrives
			continue
		default:
			// Could be a partial multi-byte response — wait for more data
			logDebugf("Unrecognised byte %02X — waiting for more data", buffer[0])
		}

	}
	if readLoops >= readLoopsLimit {
		logDebugf("Timed out — buffer: %d bytes\n%s", len(buffer), hex.Dump(buffer))
		return nil, errors.New("MEMS 1.x timed out")
	}
	logDebug("Read loop exited normally")

	return nil, nil

}

func ecu1xParseData80(data []byte) {
	globalDataOutputLock.Lock()
	defer globalDataOutputLock.Unlock()

	faults := []string{}
	logDebugf("data 0x80: %d bytes\n%s", len(data), hex.Dump(data))

	// data[0] is the command (0x80)
	data = data[1:]

	packetSize := int(data[0])
	// 14 bytes length for mems 1.3
	// ? bytes length for mems 1.6

	// byte 1-2(16 bit) - engine speed in RPM
	globalDataOutput["rpm"] = float32((int(data[1]) << 8) + int(data[2]))

	// // byte 3 - coolant temp (+55 offset and 8 bit wrap)
	globalDataOutput["coolant_temp"] = float32(data[3]) - 55
	if data[3] == 59 {
		faults = append(faults, "fault_coolant_temp_value")
	}

	// //TODO:  byte 4 - (computed) ambient temp (+55 offset, 8 bit wrap) - doesn't work, might on MPI?
	globalDataOutput["ambient_temp"] = float32(data[4]) - 55
	if data[4] == 200 {
		faults = append(faults, "fault_ambient_temp_value")
	}

	// // byte 5 - intake air temp (+55 offset, 8 bit wrap)
	globalDataOutput["intake_air_temp"] = float32(data[5]) - 55
	if data[5] == 35 {
		faults = append(faults, "fault_intake_air_temp_value")
	}

	// // byte 6 - fuel temp - doesn't work on SPI, do for MPI? # defaults to FF
	globalDataOutput["fuel_rail_temp"] = float32(data[6]) - 55

	// // byte 7 - map sensor kpa
	globalDataOutput["map_sensor_kpa"] = float32(data[7])

	// // byte 8 - battery voltage
	globalDataOutput["battery_voltage"] = float32(data[8]) / 10

	// // byte 9 - throttle pot voltage, WOT should be about 5v, should closed be near 0v? 0.02V per LSB. WOT should probably be close to 0xFA or 5.0V.
	globalDataOutput["throttle_pot_voltage"] = float32(data[9]) / 200
	//
	// // byte 10(A) - idle switch (bit 4 set if throttle closed)
	globalDataOutput["idle_switch"] = float32((int(data[10]) & 0x00001000) >> 3)
	//
	// // byte 11(B) - unknown, Probably a bitfield. Observed as 0x24 with engine off, and 0x20 with engine running. A single sample during a fifteen minute test drive showed a value of 0x30.
	//
	// // byte 12(C) - Park/neutral switch. Zero is closed, nonzero is open
	globalDataOutput["park_or_neutral_switch"] = float32(data[12])
	//
	// byte 13(D) - faults on mini spi
	// output['fault_1_bits'] = data[13].toString(2);
	// while (output['fault_1_bits'].length < 8) {
	//   output['fault_1_bits'] = "0"+output['fault_1_bits'];
	// }
	//  output['fault_1_bits'] = {"name": "Fault byte 1 bits", "data": output['fault_1_bits']};
	//
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

	// // byte 14(E) - fault codes
	// output['fault_2_bits'] = data[14].toString(2);
	// while (output['fault_2_bits'].length < 8) {
	//   output['fault_2_bits'] = "0"+output['fault_2_bits'];
	// }
	// output['fault_2_bits'] = {"name": "Fault byte 2 bits", "data": output['fault_2_bits']};
	//
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

	// // 15(F) idle setting - x6.1
	if packetSize > 15 {
		globalDataOutput["idle_setpoint"] = float32(data[15]) * 6.1
	}
	// // 16 (10) idle hotdb
	if packetSize > 16 {
		globalDataOutput["idle_hotdb"] = float32(data[16])
	}
	// // 17 (11) unknown
	if packetSize > 17 {
		logDebug("Unknown byte 17: " + hex.Dump(data[17:]))
	}

	// // 18 (x12) - idle air control motor position - 0 closed, 180 fully open
	if packetSize > 0x12 {
		globalDataOutput["idle_valve_position"] = float32(data[0x12])
	}
	// // 19-20 (x13-14) - idle speed deviation (16 bits)
	if packetSize > 0x14 {
		idleDeviation := int(data[0x13]) << 8
		idleDeviation += int(data[0x14])
		globalDataOutput["idle_speed_deviation"] = float32(idleDeviation)
	}
	// // 21 (x15) unknown
	if packetSize > 0x15 {
		globalDataOutput["ignition_advance_offset"] = float32(data[0x15])
	}
	// // 22 (x16) - ignition advance 0.5 degrees per lsb, range -24 deg (00) to 103.5 deg (0xFF)
	if packetSize > 0x16 {
		globalDataOutput["ignition_advance_raw"] = float32(data[0x16])
		globalDataOutput["ignition_advance"] = float32(data[0x16] / 2)
	}

	// // TODO: 23-24 (x17-18) - coil time 0.002ms per lsb (16 bit)
	if packetSize > 0x18 {
		coilTime := int(data[0x17]) << 8
		coilTime += int(data[0x18])
		// 0.002ms per LSB = 2 microseconds per count
		globalDataOutput["coil_time_microseconds"] = float32(coilTime) * 2
	}
	// // 25 (x19) unknown
	if packetSize > 0x19 {
		logDebug("Unknown byte 19: " + hex.Dump(data[19:]))
	}
	// // 26 (x1a) unknown
	if packetSize > 0x1a {
		logDebug("Unknown byte 20: " + hex.Dump(data[20:]))
	}
	// // 27 (x1B) unknown
	if packetSize > 0x1b {
		logDebug("Unknown byte 21: " + hex.Dump(data[21:]))
	}

	globalFaults = faults
}

func ecu1xParseData7D(data []byte) {
	globalDataOutputLock.Lock()
	defer globalDataOutputLock.Unlock()
	logDebug("ECU 1.x data 7D: " + hex.Dump(data))
	// data[0] is the command (0x7D)
	data = data[1:]
	packetSize := int(data[0])

	globalDataOutput["ignition_switch"] = float32(data[1])
	globalDataOutput["throttle_angle"] = float32(data[2]) / 2
	// 0x03  Unknown
	globalDataOutput["air_fuel_ratio"] = float32(data[4]) // "A/F ratio? might just be 0xFF (unknown)" ## if it's FF then don't output?

	dtcByte := int(data[5])
	globalDataOutput["lambda_heater_relay"] = float32((dtcByte >> 3) & 1)
	globalDataOutput["secondary_trigger_sync"] = float32((dtcByte >> 4) & 1)
	globalDataOutput["fan_1_control"] = float32((dtcByte >> 5) & 1)
	globalDataOutput["fan_2_control"] = float32((dtcByte >> 7) & 1)
	globalDataOutput["lambda_mv"] = float32(data[6]) * 5
	globalDataOutput["lambda_sensor_frequency"] = float32(data[7])
	globalDataOutput["lambda_sensor_duty_cycle"] = float32(data[8])
	globalDataOutput["lambda_sensor_status"] = float32(data[9]) // "Lambda sensor status? 0x01 for good, any other value for no good"
	if int(data[10]) > 0 {
		globalDataOutput["closed_loop"] = 1 // "Loop indicator, 0 for open loop and nonzero for closed loop"
	} else {
		globalDataOutput["closed_loop"] = 0
	}

	globalDataOutput["long_term_trim"] = float32(data[11])
	globalDataOutput["short_term_trim_percent"] = float32(data[12])
	globalDataOutput["carbon_can_purge_valve_duty_cycle"] = float32(data[13]) // "Carbon canister purge valve duty cycle?"
	dtc2 := int(data[0xE])
	globalDataOutput["primary_trigger_sync"] = float32((dtc2 >> 1) & 1)

	if packetSize >= 16 {
		globalDataOutput["idle_base_position"] = float32(data[15])
	}

	// 0x10  Unknown
	// 0x11  Unknown
	// 0x12  Unknown
	// 0x13  Unknown
	if packetSize >= 21 {
		globalDataOutput["idle_error"] = float32(data[20])
	}
	// 0x15  Unknown

	if packetSize >= 0x16 {
		dtc3 := int(data[0x16])
		globalDataOutput["injector_1_4_driver"] = float32((dtc3 >> 1) & 1)
		globalDataOutput["injector_2_3_driver"] = float32((dtc3 >> 2) & 1)
		globalDataOutput["fault_engine_bay_vent_warning"] = float32((dtc3 >> 3) & 1)
		globalDataOutput["engine_bay_vent_relay"] = float32((dtc3 >> 4) & 1)
		globalDataOutput["hill_assist"] = float32((dtc3 >> 5) & 1)
		globalDataOutput["cruise_control"] = float32((dtc3 >> 6) & 1)
	}

	// 0x17  Unknown
	// 0x18  Unknown
	// 0x19  Unknown
	// 0x1A  Unknown
	// 0x1B  Unknown
	// 0x1C  Unknown
	// 0x1D  Unknown
	// 0x1E  Unknown
	if packetSize >= 0x1F {
		globalDataOutput["crank_counter"] = float32(data[0x1F])
	}

}
