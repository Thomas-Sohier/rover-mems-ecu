package main

import (
	"fmt"
	"math"
	"math/rand"
	"time"
)

// fakeEcuLoop generates realistic-looking ECU data without any hardware.
// It simulates an engine warming up at idle with gentle sensor noise.
// It runs until the process is killed or globalUserCommand == "stop".
func fakeEcuLoop() error {
	fmt.Println("FAKE ECU: starting simulation")
	logDebug("Fake ECU loop started")

	// Warm-up state: coolant starts cold and climbs to operating temp
	coolant := -10.0  // °C at start
	oilTemp := -5.0
	rpm := 1200.0     // fast idle when cold
	tick := 0

	globalDataOutputLock.Lock()
	globalConnected = true
	globalSelectedSerialPort = "fake"
	globalSerialPorts = []string{"fake"}
	globalFaults = []string{}
	globalDataOutputLock.Unlock()

	for {
		tick++
		t := float64(tick)

		// ── Warm-up dynamics ────────────────────────────────────────────
		// Coolant rises from -10 → ~90°C over ~3 minutes, then holds
		if coolant < 88 {
			coolant += 0.08 + rand.Float64()*0.02
		} else {
			coolant += (rand.Float64() - 0.5) * 0.4
			coolant = clamp(coolant, 85, 95)
		}
		// Oil temp lags coolant by ~30%
		oilTemp += (coolant - oilTemp) * 0.003
		oilTemp += (rand.Float64() - 0.5) * 0.3

		// RPM: high when cold, drops to ~800 once warm, gentle oscillation
		targetRpm := 800.0
		if coolant < 60 {
			targetRpm = 1100 + (60-coolant)*7
		}
		targetRpm += math.Sin(t/30)*15 + (rand.Float64()-0.5)*20
		rpm += (targetRpm - rpm) * 0.05

		// ── Derived values ───────────────────────────────────────────────
		// MAP: inversely related to RPM at idle (vacuum increases with RPM)
		mapKpa := 98 - (rpm-800)*0.015 + math.Sin(t/8)*2 + (rand.Float64()-0.5)*1.5
		mapKpa = clamp(mapKpa, 30, 105)

		// Throttle: tiny noise at idle
		throttlePot := 0.08 + math.Sin(t/120)*0.02 + rand.Float64()*0.01
		throttleAngle := throttlePot * 90

		// Lambda oscillates around stoich (450 mV) in closed loop
		lambdaMv := 450 + math.Sin(t/4)*180 + (rand.Float64()-0.5)*30

		// Battery: alternator keeps it ~14.1 V, slight droop at cranking
		battery := 14.1 + math.Sin(t/200)*0.3 + (rand.Float64()-0.5)*0.05

		// Ignition advance: more advance when warm
		ignAdv := 10.0
		if coolant > 60 {
			ignAdv = 15 + (coolant-60)*0.1
		}
		ignAdv += math.Sin(t/20) * 1.5

		// Idle valve: more open when cold to support high idle
		idleValve := 60 + (targetRpm-800)*0.04 + (rand.Float64()-0.5)*3
		idleValve = clamp(idleValve, 20, 160)

		// Fuel trims: drift slightly
		longTrim := 128 + math.Sin(t/300)*4 + (rand.Float64()-0.5)*0.5
		shortTrim := 50 + math.Sin(t/5)*8 + (rand.Float64()-0.5)*2

		// Coil time
		coilTime := 2800 + math.Sin(t/15)*100 + (rand.Float64()-0.5)*50

		// Intake air: follows ambient loosely
		intakeAirTemp := 18.0 + math.Sin(t/400)*3 + (rand.Float64()-0.5)*0.5
		ambientTemp := 17.0 + (rand.Float64()-0.5)*0.2

		// ── Faults: clear by default, inject a random transient rarely ──
		faults := []string{}
		if tick%600 == 0 && rand.Float64() < 0.3 {
			faults = append(faults, "fault_knock_detected")
			logDebug("Fake ECU: transient knock fault injected")
		}

		// ── Write state ──────────────────────────────────────────────────
		globalDataOutputLock.Lock()

		globalDataOutput["rpm"] = float32(rpm)
		globalDataOutput["coolant_temp"] = float32(coolant)
		globalDataOutput["oil_temp"] = float32(oilTemp)
		globalDataOutput["intake_air_temp"] = float32(intakeAirTemp)
		globalDataOutput["ambient_temp"] = float32(ambientTemp)
		globalDataOutput["fuel_rail_temp"] = float32(coolant*0.7 + 10)
		globalDataOutput["map_sensor_kpa"] = float32(mapKpa)
		globalDataOutput["battery_voltage"] = float32(battery)
		globalDataOutput["throttle_pot_voltage"] = float32(throttlePot)
		globalDataOutput["throttle_angle"] = float32(throttleAngle)
		globalDataOutput["lambda_mv"] = float32(lambdaMv)
		globalDataOutput["lambda_sensor_frequency"] = float32(8 + rand.Float64()*2)
		globalDataOutput["lambda_sensor_duty_cycle"] = float32(50 + math.Sin(t/4)*15)
		globalDataOutput["lambda_sensor_status"] = 1
		globalDataOutput["long_term_trim"] = float32(longTrim)
		globalDataOutput["short_term_trim_percent"] = float32(shortTrim)
		globalDataOutput["carbon_can_purge_valve_duty_cycle"] = float32(5 + rand.Float64()*3)
		globalDataOutput["closed_loop"] = boolToFloat(coolant > 50)
		globalDataOutput["ignition_advance"] = float32(ignAdv)
		globalDataOutput["ignition_advance_offset"] = float32(0)
		globalDataOutput["coil_time_microseconds"] = float32(coilTime)
		globalDataOutput["coil_1_charge_time"] = float32(2.8 + rand.Float64()*0.1)
		globalDataOutput["coil_2_charge_time"] = float32(2.8 + rand.Float64()*0.1)
		globalDataOutput["idle_valve_position"] = float32(idleValve)
		globalDataOutput["idle_setpoint"] = float32(targetRpm)
		globalDataOutput["idle_speed_deviation"] = float32(rpm - targetRpm)
		globalDataOutput["idle_error"] = float32(math.Abs(rpm - targetRpm))
		globalDataOutput["idle_base_position"] = float32(55 + (rand.Float64()-0.5)*2)
		globalDataOutput["idle_timing_offset"] = float32(math.Sin(t/40) * 2)
		globalDataOutput["idle_adjuster_rpm"] = float32(targetRpm + (rand.Float64()-0.5)*10)
		globalDataOutput["vehicle_speed"] = 0
		globalDataOutput["crank_counter"] = float32(tick % 256)
		globalDataOutput["cam_percent"] = float32(50 + math.Sin(t/60)*5)
		globalDataOutput["rpm_error"] = float32(rpm - targetRpm)
		globalDataOutput["primary_trigger_sync"] = 1
		globalDataOutput["secondary_trigger_sync"] = 1
		globalDataOutput["fan_1_control"] = boolToFloat(coolant > 92)
		globalDataOutput["fan_2_control"] = 0
		globalDataOutput["lambda_heater_relay"] = boolToFloat(coolant > 30)
		globalDataOutput["idle_switch"] = boolToFloat(throttlePot < 0.12)
		globalDataOutput["ignition_switch"] = 1
		globalDataOutput["ignition"] = 1
		globalDataOutput["park_or_neutral_switch"] = 0
		globalDataOutput["throttle_switch"] = boolToFloat(throttlePot < 0.12)
		globalDataOutput["ac_button"] = 0
		globalDataOutput["injector_1_pw"] = float32(3 + (rpm/1000)*0.8 + rand.Float64()*0.2)
		globalDataOutput["injector_2_pw"] = float32(3 + (rpm/1000)*0.8 + rand.Float64()*0.2)
		globalDataOutput["injector_3_pw"] = float32(3 + (rpm/1000)*0.8 + rand.Float64()*0.2)
		globalDataOutput["injector_4_pw"] = float32(3 + (rpm/1000)*0.8 + rand.Float64()*0.2)
		globalDataOutput["injector_1_4_driver"] = 1
		globalDataOutput["injector_2_3_driver"] = 1
		globalDataOutput["estimate_air_fuel"] = float32(14.2 + math.Sin(t/4)*0.4)
		globalDataOutput["o2_mv"] = float32(lambdaMv)
		globalDataOutput["fuelling_feedback_percent"] = float32(shortTrim)

		globalFaults = faults
		globalDataOutputLock.Unlock()

		// Handle user commands
		globalDataOutputLock.Lock()
		cmd := globalUserCommand
		if cmd != "" {
			globalUserCommand = ""
			logDebug("Fake ECU: received command: " + cmd)
			if cmd == "clearfaults" {
				globalFaults = []string{}
				globalAlert = "ECU reports faults cleared"
				logDebug("Fake ECU: faults cleared")
			} else {
				globalAlert = "Fake ECU accepted: " + cmd
			}
		}
		globalDataOutputLock.Unlock()

		time.Sleep(100 * time.Millisecond) // 10 Hz update rate
	}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func boolToFloat(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
