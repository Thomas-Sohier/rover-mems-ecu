package fake

import (
	"math"
	"math/rand"
	"time"

	"rover-mems-agent/internal/ecu"
)

func init() {
	ecu.Register("fake", NewFakeECU)
}

// FakeECU simulates an ECU for testing without hardware.
type FakeECU struct {
	state   *ecu.State
	running bool
}

// NewFakeECU creates a new fake ECU instance.
func NewFakeECU(state *ecu.State, cfg ecu.Config) (ecu.ECU, error) {
	state.DebugMode = cfg.DebugMode
	return &FakeECU{state: state}, nil
}

func (f *FakeECU) Connect(portName string) error {
	f.state.Lock()
	f.state.Connected = true
	f.state.Faults = []string{}
	f.state.Unlock()
	f.running = true
	return nil
}

func (f *FakeECU) ReadData() error {
	f.state.LogDebug("Fake ECU: starting simulation")

	coolant := -10.0
	oilTemp := -5.0
	rpm := 1200.0
	tick := 0

	for f.running {
		tick++
		t := float64(tick)

		if coolant < 88 {
			coolant += 0.08 + rand.Float64()*0.02
		} else {
			coolant += (rand.Float64() - 0.5) * 0.4
			coolant = clamp(coolant, 85, 95)
		}
		oilTemp += (coolant - oilTemp) * 0.003
		oilTemp += (rand.Float64() - 0.5) * 0.3

		targetRpm := 800.0
		if coolant < 60 {
			targetRpm = 1100 + (60-coolant)*7
		}
		targetRpm += math.Sin(t/30)*15 + (rand.Float64()-0.5)*20
		rpm += (targetRpm - rpm) * 0.05

		mapKpa := 98 - (rpm-800)*0.015 + math.Sin(t/8)*2 + (rand.Float64()-0.5)*1.5
		mapKpa = clamp(mapKpa, 30, 105)
		throttlePot := 0.08 + math.Sin(t/120)*0.02 + rand.Float64()*0.01
		throttleAngle := throttlePot * 90
		lambdaMv := 450 + math.Sin(t/4)*180 + (rand.Float64()-0.5)*30
		battery := 14.1 + math.Sin(t/200)*0.3 + (rand.Float64()-0.5)*0.05
		ignAdv := 10.0
		if coolant > 60 {
			ignAdv = 15 + (coolant-60)*0.1
		}
		ignAdv += math.Sin(t/20) * 1.5
		idleValve := 60 + (targetRpm-800)*0.04 + (rand.Float64()-0.5)*3
		idleValve = clamp(idleValve, 20, 160)
		longTrim := 128 + math.Sin(t/300)*4 + (rand.Float64()-0.5)*0.5
		shortTrim := 50 + math.Sin(t/5)*8 + (rand.Float64()-0.5)*2
		coilTime := 2800 + math.Sin(t/15)*100 + (rand.Float64()-0.5)*50
		intakeAirTemp := 18.0 + math.Sin(t/400)*3 + (rand.Float64()-0.5)*0.5
		ambientTemp := 17.0 + (rand.Float64()-0.5)*0.2

		faults := []string{}
		if tick%600 == 0 && rand.Float64() < 0.3 {
			faults = append(faults, "fault_knock_detected")
			f.state.LogDebug("Fake ECU: transient knock fault injected")
		}

		f.state.Lock()
		f.state.Data["rpm"] = float32(rpm)
		f.state.Data["coolant_temp"] = float32(coolant)
		f.state.Data["oil_temp"] = float32(oilTemp)
		f.state.Data["intake_air_temp"] = float32(intakeAirTemp)
		f.state.Data["ambient_temp"] = float32(ambientTemp)
		f.state.Data["fuel_rail_temp"] = float32(coolant*0.7 + 10)
		f.state.Data["map_sensor_kpa"] = float32(mapKpa)
		f.state.Data["battery_voltage"] = float32(battery)
		f.state.Data["throttle_pot_voltage"] = float32(throttlePot)
		f.state.Data["throttle_angle"] = float32(throttleAngle)
		f.state.Data["lambda_mv"] = float32(lambdaMv)
		f.state.Data["lambda_sensor_frequency"] = float32(8 + rand.Float64()*2)
		f.state.Data["lambda_sensor_duty_cycle"] = float32(50 + math.Sin(t/4)*15)
		f.state.Data["lambda_sensor_status"] = 1
		f.state.Data["long_term_trim"] = float32(longTrim)
		f.state.Data["short_term_trim_percent"] = float32(shortTrim)
		f.state.Data["carbon_can_purge_valve_duty_cycle"] = float32(5 + rand.Float64()*3)
		f.state.Data["closed_loop"] = boolToFloat(coolant > 50)
		f.state.Data["ignition_advance"] = float32(ignAdv)
		f.state.Data["ignition_advance_offset"] = float32(0)
		f.state.Data["coil_time_microseconds"] = float32(coilTime)
		f.state.Data["coil_1_charge_time"] = float32(2.8 + rand.Float64()*0.1)
		f.state.Data["coil_2_charge_time"] = float32(2.8 + rand.Float64()*0.1)
		f.state.Data["idle_valve_position"] = float32(idleValve)
		f.state.Data["idle_setpoint"] = float32(targetRpm)
		f.state.Data["idle_speed_deviation"] = float32(rpm - targetRpm)
		f.state.Data["idle_error"] = float32(math.Abs(rpm - targetRpm))
		f.state.Data["idle_base_position"] = float32(55 + (rand.Float64()-0.5)*2)
		f.state.Data["idle_timing_offset"] = float32(math.Sin(t/40) * 2)
		f.state.Data["idle_adjuster_rpm"] = float32(targetRpm + (rand.Float64()-0.5)*10)
		f.state.Data["vehicle_speed"] = 0
		f.state.Data["crank_counter"] = float32(tick % 256)
		f.state.Data["cam_percent"] = float32(50 + math.Sin(t/60)*5)
		f.state.Data["rpm_error"] = float32(rpm - targetRpm)
		f.state.Data["primary_trigger_sync"] = 1
		f.state.Data["secondary_trigger_sync"] = 1
		f.state.Data["fan_1_control"] = boolToFloat(coolant > 92)
		f.state.Data["fan_2_control"] = 0
		f.state.Data["lambda_heater_relay"] = boolToFloat(coolant > 30)
		f.state.Data["idle_switch"] = boolToFloat(throttlePot < 0.12)
		f.state.Data["ignition_switch"] = 1
		f.state.Data["ignition"] = 1
		f.state.Data["park_or_neutral_switch"] = 0
		f.state.Data["throttle_switch"] = boolToFloat(throttlePot < 0.12)
		f.state.Data["ac_button"] = 0
		f.state.Data["injector_1_pw"] = float32(3 + (rpm/1000)*0.8 + rand.Float64()*0.2)
		f.state.Data["injector_2_pw"] = float32(3 + (rpm/1000)*0.8 + rand.Float64()*0.2)
		f.state.Data["injector_3_pw"] = float32(3 + (rpm/1000)*0.8 + rand.Float64()*0.2)
		f.state.Data["injector_4_pw"] = float32(3 + (rpm/1000)*0.8 + rand.Float64()*0.2)
		f.state.Data["injector_1_4_driver"] = 1
		f.state.Data["injector_2_3_driver"] = 1
		f.state.Data["estimate_air_fuel"] = float32(14.2 + math.Sin(t/4)*0.4)
		f.state.Data["o2_mv"] = float32(lambdaMv)
		f.state.Data["fuelling_feedback_percent"] = float32(shortTrim)
		f.state.Faults = faults

		cmd := f.state.UserCommand
		if cmd != "" {
			f.state.UserCommand = ""
			f.state.LogDebug("Fake ECU: received command: " + cmd)
			if cmd == "clearfaults" {
				f.state.Faults = []string{}
				f.state.Alert = "ECU reports faults cleared"
			} else {
				f.state.Alert = "Fake ECU accepted: " + cmd
			}
		}
		f.state.Unlock()

		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func (f *FakeECU) GetFaults() []string {
	f.state.RLock()
	defer f.state.RUnlock()
	return f.state.Faults
}

func (f *FakeECU) SendCommand(cmd string) error {
	f.state.Lock()
	f.state.UserCommand = cmd
	f.state.Unlock()
	return nil
}

func (f *FakeECU) Close() error {
	f.running = false
	f.state.Lock()
	f.state.Connected = false
	f.state.Unlock()
	return nil
}

func (f *FakeECU) Type() string {
	return "fake"
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
