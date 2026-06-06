package main

import (
	"context"
	"errors"
	"testing"

	"rover-mems-agent/internal/ecu"
)

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantPort  string
		wantSer   string
		wantEcu   string
		wantDebug bool
	}{
		{"defaults", nil, ":8080", "", "", false},
		{"all set", []string{"-serialport", "/dev/ttyUSB0", "-ecutype", "1.9", "-mode", "debug", "-port", "9000"}, ":9000", "/dev/ttyUSB0", "1.9", true},
		{"prod mode leaves debug off", []string{"-mode", "prod"}, ":8080", "", "", false},
		{"only ecutype", []string{"-ecutype", "fake"}, ":8080", "", "fake", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := ecu.NewState()
			gotPort := parseFlags(state, tt.args)

			if gotPort != tt.wantPort {
				t.Errorf("httpPort = %q, want %q", gotPort, tt.wantPort)
			}
			if state.SelectedSerialPort != tt.wantSer {
				t.Errorf("SelectedSerialPort = %q, want %q", state.SelectedSerialPort, tt.wantSer)
			}
			if state.EcuType != tt.wantEcu {
				t.Errorf("EcuType = %q, want %q", state.EcuType, tt.wantEcu)
			}
			if state.DebugMode != tt.wantDebug {
				t.Errorf("DebugMode = %v, want %v", state.DebugMode, tt.wantDebug)
			}
		})
	}
}

// stubPorts swaps the getPorts seam for the duration of a test.
func stubPorts(t *testing.T, list []string, err error) {
	t.Helper()
	orig := getPorts
	getPorts = func() ([]string, error) { return list, err }
	t.Cleanup(func() { getPorts = orig })
}

func TestConnectLoop_NoEcuType(t *testing.T) {
	state := ecu.NewState()
	if err := connectLoop(context.Background(), state); err == nil {
		t.Fatal("expected error when no ECU type is selected")
	}
}

func TestConnectLoop_PortSelection(t *testing.T) {
	tests := []struct {
		name       string
		selected   string
		ports      []string
		portsErr   error
		wantErr    bool
		wantPicked string // expected SelectedSerialPort after the call (when auto-selected)
	}{
		{"no ports found", "", []string{}, nil, true, ""},
		{"ports list error", "", nil, errors.New("enumeration failed"), true, ""},
		{"auto-select single port", "", []string{"/dev/fake-port-a"}, nil, true, "/dev/fake-port-a"},
		{"auto-select first of many", "", []string{"/dev/fake-port-a", "/dev/fake-port-b"}, nil, true, "/dev/fake-port-a"},
		{"keep explicit selection", "/dev/fake-port-z", []string{"/dev/fake-port-a"}, nil, true, "/dev/fake-port-z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubPorts(t, tt.ports, tt.portsErr)

			state := ecu.NewState()
			state.EcuType = "1.9" // any real type; Factory→Connect will fail on the fake port name
			state.SelectedSerialPort = tt.selected

			// connectLoop proceeds to runECU, which opens tt.wantPicked and fails
			// (no hardware). We only assert the selection side effect and that an
			// error surfaces, not the downstream serial failure detail.
			err := connectLoop(context.Background(), state)
			if tt.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if tt.wantPicked != "" && state.SelectedSerialPort != tt.wantPicked {
				t.Errorf("SelectedSerialPort = %q, want %q", state.SelectedSerialPort, tt.wantPicked)
			}
		})
	}
}

func TestConnectLoop_FakeEcu(t *testing.T) {
	// The fake ECU runs without serial hardware; a cancelled context makes
	// ReadData return promptly so connectLoop terminates.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	state := ecu.NewState()
	state.EcuType = "fake"

	err := connectLoop(ctx, state)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}
