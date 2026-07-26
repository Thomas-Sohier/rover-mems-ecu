package mems3

import (
	"math"
	"testing"

	"rover-mems-agent/internal/ecu"
	"rover-mems-agent/internal/ecu/serialtest"
)

func newTestHandler() (*MEMS3, *serialtest.FakePort) {
	port := serialtest.NewFakePort()
	return &MEMS3{state: ecu.NewState(), sp: port}, port
}

func approx(got, want float32) bool {
	return math.Abs(float64(got-want)) < 1e-4
}

// framed wraps a payload the way sendCommand does:
// [B8 13 F7][len][payload][XOR of everything before it].
func framed(payload ...byte) []byte {
	out := append([]byte{}, requestHeader...)
	out = append(out, byte(len(payload)))
	out = append(out, payload...)
	return append(out, xorAllBytes(out))
}

// TestSendNextCommand transcribes the transitions from the if/else chain the
// nextAfterResponse table replaced.
func TestSendNextCommand(t *testing.T) {
	tests := []struct {
		name     string
		previous []byte
		want     []byte
	}{
		{"init accepted -> start diagnostic", initAccepted, startDiagnostic},
		{"diag accepted -> request seed", startDiagResponse, requestSeed},
		{"key accepted -> ping", keyAcceptResponse, pingCommand},
		{"pong -> request faults", pongResponse, requestFaultsCommand},
		{"faults cleared -> request faults", faultsClearedResponse, requestFaultsCommand},
		{"faults -> data 00", responseFaults, requestData00},
		{"data 00 -> data 06", responseData00, requestData06},
		{"data 06 -> data 0A", responseData06, requestData0A},
		{"data 0A -> data 0B", responseData0A, requestData0B},
		{"data 0B -> data 21", responseData0B, requestData21},
		{"data 21 wraps to ping", responseData21, pingCommand},
		{"unknown falls back to ping", []byte{0xAB, 0xCD}, pingCommand},
		{"nil falls back to ping", nil, pingCommand},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, port := newTestHandler()

			m.sendNextCommand(tc.previous)

			want := framed(tc.want...)
			if string(port.Written) != string(want) {
				t.Errorf("sent % X, want % X", port.Written, want)
			}
		})
	}
}

func TestSendNextCommand_SeedResponseSendsKey(t *testing.T) {
	m, port := newTestHandler()
	m.key = 0xCAFE

	m.sendNextCommand(seedResponse)

	want := framed(0x27, 0x02, 0xCA, 0xFE)
	if string(port.Written) != string(want) {
		t.Errorf("sent % X, want % X", port.Written, want)
	}
}

// TestSendCommandDoesNotCorruptSharedSlices guards both package-level literals
// that were previously used as append targets.
func TestSendCommandDoesNotCorruptSharedSlices(t *testing.T) {
	headerBefore := string(requestHeader)
	keyBefore := string(sendKey)

	m, _ := newTestHandler()
	m.key = 0x1234
	m.sendNextCommand(seedResponse)
	m.sendCommand(pingCommand)
	m.sendCommand(requestFaultsCommand)

	if got := string(requestHeader); got != headerBefore {
		t.Errorf("requestHeader mutated: % X", requestHeader)
	}
	if got := string(sendKey); got != keyBefore {
		t.Errorf("sendKey mutated: % X", sendKey)
	}
}

// TestSendNextCommand_UnknownUserCommandIsCleared covers a behaviour fix: the
// old code logged an unrecognised name but left it pending, so it was retried
// on every single iteration for the rest of the session.
func TestSendNextCommand_UnknownUserCommandIsCleared(t *testing.T) {
	m, port := newTestHandler()
	m.state.SetUserCommand("not-a-command")

	m.sendNextCommand(pongResponse)

	if got := m.state.Snapshot().UserCommand; got != "" {
		t.Errorf("UserCommand = %q, want cleared", got)
	}
	if want := framed(requestFaultsCommand...); string(port.Written) != string(want) {
		t.Errorf("sent % X, want % X", port.Written, want)
	}
}

func TestSendNextCommand_UserCommandTakesPriority(t *testing.T) {
	m, port := newTestHandler()
	m.state.SetUserCommand("clearfaults")

	m.sendNextCommand(pongResponse)

	if want := framed(clearFaultsCommand...); string(port.Written) != string(want) {
		t.Errorf("sent % X, want clear-faults % X", port.Written, want)
	}
}

// --- handleFrame ---

func TestHandleFrame_Data00(t *testing.T) {
	m, _ := newTestHandler()
	// coolant 2830 -> 10.0 C, oil 2930 -> 20.0 C, intake 3030 -> 30.0 C
	frame := []byte{
		0x61, 0x00,
		0x0B, 0x0E, // 2830
		0x00, 0x00,
		0x0B, 0x72, // 2930
		0x00, 0x00,
		0x0B, 0xD6, // 3030
	}

	reply, ok := m.handleFrame(frame)
	if !ok {
		t.Fatal("frame not handled")
	}
	if string(reply) != string(responseData00) {
		t.Errorf("reply = % X, want % X", reply, responseData00)
	}

	data := m.state.Snapshot().Data
	if !approx(data["coolant_temp"], 10) {
		t.Errorf("coolant_temp = %v, want 10", data["coolant_temp"])
	}
	if !approx(data["oil_temp"], 20) {
		t.Errorf("oil_temp = %v, want 20", data["oil_temp"])
	}
	if !approx(data["intake_air_temp"], 30) {
		t.Errorf("intake_air_temp = %v, want 30", data["intake_air_temp"])
	}
}

func TestHandleFrame_Data06(t *testing.T) {
	m, _ := newTestHandler()
	frame := []byte{
		0x61, 0x06,
		0x27, 0x10, // 10000 -> 100.00 kPa
		0x00, 0x00,
		0x00, 0x00,
		0x03, 0xE8, // throttle 1000 mV
		0x0F, 0xA0, // rpm 4000
	}

	reply, ok := m.handleFrame(frame)
	if !ok {
		t.Fatal("frame not handled")
	}
	if string(reply) != string(responseData06) {
		t.Errorf("reply = % X, want % X", reply, responseData06)
	}

	data := m.state.Snapshot().Data
	if !approx(data["map_sensor_kpa"], 100) {
		t.Errorf("map_sensor_kpa = %v, want 100", data["map_sensor_kpa"])
	}
	if !approx(data["throttle_mv"], 1000) {
		t.Errorf("throttle_mv = %v, want 1000", data["throttle_mv"])
	}
	if !approx(data["rpm"], 4000) {
		t.Errorf("rpm = %v, want 4000", data["rpm"])
	}
}

func TestHandleFrame_InitAcceptedMarksConnected(t *testing.T) {
	m, _ := newTestHandler()

	reply, ok := m.handleFrame(initAccepted)
	if !ok {
		t.Fatal("frame not handled")
	}
	if string(reply) != string(initAccepted) {
		t.Errorf("reply = % X, want % X", reply, initAccepted)
	}
	if !m.state.IsConnected() {
		t.Error("Connected = false, want true")
	}
}

// TestHandleFrame_ZeroSeedSkipsAuth covers the "already unlocked" path: no key
// is derived and the nil reply falls through to a ping.
func TestHandleFrame_ZeroSeedSkipsAuth(t *testing.T) {
	m, port := newTestHandler()

	reply, ok := m.handleFrame([]byte{0x67, 0x01, 0x00, 0x00})
	if !ok {
		t.Fatal("frame not handled")
	}
	if reply != nil {
		t.Errorf("reply = % X, want nil", reply)
	}
	if m.key != 0 {
		t.Errorf("key = %d, want 0", m.key)
	}

	m.sendNextCommand(reply)
	if want := framed(pingCommand...); string(port.Written) != string(want) {
		t.Errorf("sent % X, want ping % X", port.Written, want)
	}
}

func TestHandleFrame_NonZeroSeedDerivesKey(t *testing.T) {
	m, _ := newTestHandler()

	reply, ok := m.handleFrame([]byte{0x67, 0x01, 0x12, 0x34})
	if !ok {
		t.Fatal("frame not handled")
	}
	if string(reply) != string(seedResponse) {
		t.Errorf("reply = % X, want % X", reply, seedResponse)
	}
	if m.seed != 0x1234 {
		t.Errorf("seed = %#x, want 0x1234", m.seed)
	}
	if m.key != ecu.GenerateKey(0x1234) {
		t.Errorf("key = %d, want %d", m.key, ecu.GenerateKey(0x1234))
	}
}

func TestHandleFrame_FaultsClearedRaisesAlert(t *testing.T) {
	m, _ := newTestHandler()

	if _, ok := m.handleFrame(faultsClearedResponse); !ok {
		t.Fatal("frame not handled")
	}
	if alert, _ := m.state.ConsumeAlertError(); alert == "" {
		t.Error("expected a faults-cleared alert")
	}
}

func TestHandleFrame_Unrecognised(t *testing.T) {
	m, _ := newTestHandler()

	if _, ok := m.handleFrame([]byte{0xAB, 0xCD, 0xEF}); ok {
		t.Error("unrecognised frame reported as handled")
	}
}

// TestHandleFrame_ShortDataFrames checks a truncated data PID is rejected rather
// than indexing past the payload.
func TestHandleFrame_ShortDataFrames(t *testing.T) {
	for _, header := range [][]byte{
		responseData00, responseData06, responseData0A, responseData0B, responseData21,
	} {
		m, _ := newTestHandler()
		if _, ok := m.handleFrame(header); ok {
			t.Errorf("short frame % X reported as handled", header)
		}
	}
}
