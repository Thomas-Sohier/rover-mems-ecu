package mems2j

import (
	"testing"

	"rover-mems-agent/internal/ecu"
	"rover-mems-agent/internal/ecu/serialtest"
)

// newTestHandler returns a MEMS2J wired to a fake port, plus that port.
func newTestHandler() (*MEMS2J, *serialtest.FakePort) {
	port := serialtest.NewFakePort()
	return &MEMS2J{state: ecu.NewState(), sp: port}, port
}

// framed wraps a payload the way sendCommand does: [len][payload][checksum].
func framed(payload ...byte) []byte {
	out := []byte{byte(len(payload))}
	out = append(out, payload...)
	sum := 0
	for _, b := range out {
		sum += int(b)
	}
	return append(out, byte(sum&0xFF))
}

// TestSendNextCommand covers every transition the state machine can take. The
// expectations are transcribed from the explicit if/else chain this table
// replaced, so a divergence in the derived pollOrder mapping shows up here.
func TestSendNextCommand(t *testing.T) {
	tests := []struct {
		name     string
		previous []byte
		want     []byte
	}{
		// Handshake.
		{"woke -> start diagnostic", wokeResponse, startDiagnostic},
		{"diag accepted -> request seed", startDiagResponse, requestSeed},
		{"key accepted -> ping", keyAcceptResponse, pingCommand},
		{"pong -> request faults", pongResponse, requestFaultsCommand},
		{"faults cleared -> request faults", faultsClearedResponse, requestFaultsCommand},
		{"immo learned -> data 00", responseLearnImmoCommand, requestData00},
		{"ping refused -> request seed", refusePing, requestSeed},

		// Fault list then the data-PID cycle.
		{"faults -> data 00", []byte{0x61, 0x19, 0x00}, requestData00},
		{"data 00 -> data 01", []byte{0x61, 0x00, 0xFF}, requestData01},
		{"data 01 -> data 02", []byte{0x61, 0x01, 0xFF}, requestData02},
		{"data 02 -> data 03", []byte{0x61, 0x02, 0xFF}, requestData03},
		{"data 03 -> data 05", []byte{0x61, 0x03, 0xFF}, requestData05},
		{"data 05 -> data 06", []byte{0x61, 0x05, 0xFF}, requestData06},
		{"data 06 -> data 07", []byte{0x61, 0x06, 0xFF}, requestData07},
		{"data 07 -> data 08", []byte{0x61, 0x07, 0xFF}, requestData08},
		{"data 08 -> data 09", []byte{0x61, 0x08, 0xFF}, requestData09},
		{"data 09 -> data 0A", []byte{0x61, 0x09, 0xFF}, requestData0A},
		{"data 0A -> data 0B", []byte{0x61, 0x0A, 0xFF}, requestData0B},
		{"data 0B -> data 0C", []byte{0x61, 0x0B, 0xFF}, requestData0C},
		{"data 0C -> data 0D", []byte{0x61, 0x0C, 0xFF}, requestData0D},
		{"data 0D -> data 0F", []byte{0x61, 0x0D, 0xFF}, requestData0F},
		{"data 0F -> data 10", []byte{0x61, 0x0F, 0xFF}, requestData10},
		{"data 10 -> data 11", []byte{0x61, 0x10, 0xFF}, requestData11},
		{"data 11 -> data 12", []byte{0x61, 0x11, 0xFF}, requestData12},
		{"data 12 -> data 13", []byte{0x61, 0x12, 0xFF}, requestData13},
		{"data 13 -> data 21", []byte{0x61, 0x13, 0xFF}, requestData21},
		{"data 21 -> data 25", []byte{0x61, 0x21, 0xFF}, requestData25},
		{"data 25 -> data 3A", []byte{0x61, 0x25, 0xFF}, requestData3A},
		{"data 3A wraps to ping", []byte{0x61, 0x3A, 0xFF}, pingCommand},

		// Fallback.
		{"unknown falls back to ping", []byte{0xAB, 0xCD}, pingCommand},
		{"empty falls back to ping", []byte{}, pingCommand},
		{"single byte falls back to ping", []byte{0x61}, pingCommand},
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
	m.key = 0xBEEF

	m.sendNextCommand([]byte{0x67, 0x01, 0x12, 0x34})

	want := framed(0x27, 0x02, 0xBE, 0xEF)
	if string(port.Written) != string(want) {
		t.Errorf("sent % X, want % X", port.Written, want)
	}
}

// TestSendNextCommand_SeedDoesNotCorruptSendKey guards the package-level sendKey
// slice: building the key frame by appending to it would write through to the
// shared literal the moment it had spare capacity.
func TestSendNextCommand_SeedDoesNotCorruptSendKey(t *testing.T) {
	before := string(sendKey)

	m, _ := newTestHandler()
	m.key = 0x1234
	m.sendNextCommand([]byte{0x67, 0x01, 0x00, 0x00})

	if got := string(sendKey); got != before {
		t.Errorf("sendKey mutated: % X, want % X", sendKey, []byte(before))
	}
	if len(sendKey) != 2 {
		t.Errorf("sendKey length = %d, want 2", len(sendKey))
	}
}

func TestSendNextCommand_UserCommandTakesPriority(t *testing.T) {
	m, port := newTestHandler()
	m.state.SetUserCommand("clearfaults")

	// A pong would normally request the fault list.
	m.sendNextCommand(pongResponse)

	want := framed(clearFaultsCommand...)
	if string(port.Written) != string(want) {
		t.Errorf("sent % X, want clear-faults % X", port.Written, want)
	}
	if got := m.state.Snapshot().UserCommand; got != "" {
		t.Errorf("UserCommand = %q, want cleared", got)
	}
}

// TestSendNextCommand_UnknownUserCommandIsCleared checks an unrecognised name
// cannot wedge the loop by being retried on every iteration.
func TestSendNextCommand_UnknownUserCommandIsCleared(t *testing.T) {
	m, port := newTestHandler()
	m.state.SetUserCommand("not-a-command")

	m.sendNextCommand(pongResponse)

	if got := m.state.Snapshot().UserCommand; got != "" {
		t.Errorf("UserCommand = %q, want cleared", got)
	}
	// The normal transition still happens.
	want := framed(requestFaultsCommand...)
	if string(port.Written) != string(want) {
		t.Errorf("sent % X, want % X", port.Written, want)
	}
}

// TestPollOrderCoversEveryDataPID checks the derived table has an entry for each
// polled request and that the cycle closes back onto ping.
func TestPollOrderCoversEveryDataPID(t *testing.T) {
	if len(nextAfterPoll) != len(pollOrder) {
		t.Fatalf("nextAfterPoll has %d entries, pollOrder has %d", len(nextAfterPoll), len(pollOrder))
	}
	for i, req := range pollOrder {
		key := [2]byte{0x61, req[1]}
		next, ok := nextAfterPoll[key]
		if !ok {
			t.Errorf("no transition for response % X", key)
			continue
		}
		if i == len(pollOrder)-1 {
			if string(next) != string(pingCommand) {
				t.Errorf("last poll entry goes to % X, want ping % X", next, pingCommand)
			}
			continue
		}
		if string(next) != string(pollOrder[i+1]) {
			t.Errorf("after % X: got % X, want % X", req, next, pollOrder[i+1])
		}
	}
}
