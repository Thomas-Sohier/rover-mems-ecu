package mems1x

import (
	"math"
	"slices"
	"testing"

	"rover-mems-agent/internal/ecu"
)

// makeHandler returns a minimal MEMS1x with an initialised state.
func makeHandler() *MEMS1x {
	state := ecu.NewState()
	return &MEMS1x{state: state}
}

// approx returns true when got and want agree within 1e-4.
func approx(got, want float32) bool {
	return math.Abs(float64(got-want)) < 1e-4
}

// build80 constructs a raw 0x80 frame with the given packetSize.
// The returned slice has length packetSize+2:
//
//	raw[0] = 0x80 (echo)
//	raw[1] = packetSize
//	raw[2..] = payload bytes (caller fills in what matters)
func build80(packetSize int) []byte {
	frame := make([]byte, packetSize+2)
	frame[0] = 0x80
	frame[1] = byte(packetSize)
	return frame
}

// build7D constructs a raw 0x7D frame with the given packetSize.
func build7D(packetSize int) []byte {
	frame := make([]byte, packetSize+2)
	frame[0] = 0x7D
	frame[1] = byte(packetSize)
	return frame
}

// ---------------------------------------------------------------------------
// parseData80 tests
// ---------------------------------------------------------------------------

func TestParseData80_RPM(t *testing.T) {
	m := makeHandler()
	frame := build80(0x1C)
	// data[1]=0x0A, data[2]=0x28 → raw[2]=0x0A, raw[3]=0x28
	frame[2] = 0x0A
	frame[3] = 0x28

	m.parseData80(frame)

	got := m.state.Data["rpm"]
	want := float32(2600)
	if !approx(got, want) {
		t.Errorf("rpm: got %v, want %v", got, want)
	}
}

func TestParseData80_CoolantTemp(t *testing.T) {
	m := makeHandler()
	frame := build80(0x1C)
	// data[3]=80 → raw[4]=80 → 80-55=25
	frame[4] = 80

	m.parseData80(frame)

	got := m.state.Data["coolant_temp"]
	want := float32(25)
	if !approx(got, want) {
		t.Errorf("coolant_temp: got %v, want %v", got, want)
	}
}

func TestParseData80_BatteryVoltage(t *testing.T) {
	m := makeHandler()
	frame := build80(0x1C)
	// data[8]=123 → raw[9]=123 → 123/10=12.3
	frame[9] = 123

	m.parseData80(frame)

	got := m.state.Data["battery_voltage"]
	want := float32(12.3)
	if !approx(got, want) {
		t.Errorf("battery_voltage: got %v, want %v", got, want)
	}
}

func TestParseData80_MapSensorKpa(t *testing.T) {
	m := makeHandler()
	frame := build80(0x1C)
	// data[7]=50 → raw[8]=50 → 50 (direct)
	frame[8] = 50

	m.parseData80(frame)

	got := m.state.Data["map_sensor_kpa"]
	want := float32(50)
	if !approx(got, want) {
		t.Errorf("map_sensor_kpa: got %v, want %v", got, want)
	}
}

func TestParseData80_ThrottlePotVoltage(t *testing.T) {
	// Regression: old code divided by 200, giving 250/200=1.25.
	// Fixed code divides by 50: 250/50=5.0.
	m := makeHandler()
	frame := build80(0x1C)
	// data[9]=250 → raw[10]=250
	frame[10] = 250

	m.parseData80(frame)

	got := m.state.Data["throttle_pot_voltage"]
	want := float32(5.0)
	if !approx(got, want) {
		t.Errorf("throttle_pot_voltage: got %v, want %v (regression: old code gave 1.25)", got, want)
	}
}

func TestParseData80_IdleSwitch(t *testing.T) {
	// Regression: old buggy mask &0x1000 always gave 0.
	// Fixed code: (data[10] & 0x10) >> 4.
	tests := []struct {
		name  string
		value byte
		want  float32
	}{
		{"bit4_set", 0x10, 1},
		{"bit4_clear", 0x00, 0},
		{"other_bits_set", 0xEF, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := makeHandler()
			frame := build80(0x1C)
			// data[10] → raw[11]
			frame[11] = tc.value

			m.parseData80(frame)

			got := m.state.Data["idle_switch"]
			if !approx(got, tc.want) {
				t.Errorf("idle_switch with byte=0x%02X: got %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestParseData80_IgnitionAdvance(t *testing.T) {
	// Regression: old code did integer byte/2 with no offset → 200/2=100.
	// Fixed code: float32(200)/2 - 24 = 76.0.
	m := makeHandler()
	// packetSize must be > 0x16 (22); 0x1C=28 satisfies this.
	frame := build80(0x1C)
	// data[0x16]=200 → raw[0x17]=200
	frame[0x17] = 200

	m.parseData80(frame)

	gotAdv := m.state.Data["ignition_advance"]
	wantAdv := float32(76.0)
	if !approx(gotAdv, wantAdv) {
		t.Errorf("ignition_advance: got %v, want %v", gotAdv, wantAdv)
	}

	gotRaw := m.state.Data["ignition_advance_raw"]
	wantRaw := float32(200)
	if !approx(gotRaw, wantRaw) {
		t.Errorf("ignition_advance_raw: got %v, want %v", gotRaw, wantRaw)
	}
}

func TestParseData80_CoilTimeMicroseconds(t *testing.T) {
	// data[0x17]=0x01, data[0x18]=0x00 → (1<<8+0)*2 = 512 µs
	// packetSize must be > 0x18 (24); 0x1C=28 satisfies.
	m := makeHandler()
	frame := build80(0x1C)
	// raw[0x18]=0x01, raw[0x19]=0x00
	frame[0x18] = 0x01
	frame[0x19] = 0x00

	m.parseData80(frame)

	got := m.state.Data["coil_time_microseconds"]
	want := float32(512)
	if !approx(got, want) {
		t.Errorf("coil_time_microseconds: got %v, want %v", got, want)
	}
}

func TestParseData80_FaultKnockDetected(t *testing.T) {
	// data[13] bit 6 (0x40) → "fault_knock_detected"
	m := makeHandler()
	frame := build80(0x1C)
	// data[13] → raw[14]
	frame[14] = 0x40

	m.parseData80(frame)

	if !slices.Contains(m.state.Faults, "fault_knock_detected") {
		t.Errorf("expected fault_knock_detected in %v", m.state.Faults)
	}
}

func TestParseData80_NoFaultKnock(t *testing.T) {
	m := makeHandler()
	frame := build80(0x1C)
	// data[13]=0x00 → no fault_knock_detected
	frame[14] = 0x00

	m.parseData80(frame)

	if slices.Contains(m.state.Faults, "fault_knock_detected") {
		t.Errorf("unexpected fault_knock_detected in %v", m.state.Faults)
	}
}

// ---------------------------------------------------------------------------
// parseData7D tests
// ---------------------------------------------------------------------------

func TestParseData7D_ThrottleAngle(t *testing.T) {
	// data[2]=90 → raw[3]=90 → 90/2=45
	m := makeHandler()
	frame := build7D(0x1C)
	frame[3] = 90

	m.parseData7D(frame)

	got := m.state.Data["throttle_angle"]
	want := float32(45)
	if !approx(got, want) {
		t.Errorf("throttle_angle: got %v, want %v", got, want)
	}
}

func TestParseData7D_LambdaMv(t *testing.T) {
	// data[6]=100 → raw[7]=100 → 100*5=500 (current behaviour)
	m := makeHandler()
	frame := build7D(0x1C)
	frame[7] = 100

	m.parseData7D(frame)

	got := m.state.Data["lambda_mv"]
	want := float32(500)
	if !approx(got, want) {
		t.Errorf("lambda_mv: got %v, want %v", got, want)
	}
}

func TestParseData7D_ClosedLoop(t *testing.T) {
	tests := []struct {
		name  string
		value byte
		want  float32
	}{
		{"nonzero", 1, 1},
		{"zero", 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := makeHandler()
			frame := build7D(0x1C)
			// data[10] → raw[11]
			frame[11] = tc.value

			m.parseData7D(frame)

			got := m.state.Data["closed_loop"]
			if !approx(got, tc.want) {
				t.Errorf("closed_loop with byte=%d: got %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
