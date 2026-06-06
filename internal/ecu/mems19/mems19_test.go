package mems19

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"rover-mems-agent/internal/ecu"
	"rover-mems-agent/internal/ecu/serialtest"

	"github.com/distributed/sers"
)

// newHandler builds a MEMS19 wired to the given fake port, bypassing Connect.
func newHandler(sp sers.SerialPort) *MEMS19 {
	return &MEMS19{state: ecu.NewState(), sp: sp}
}

// noSleep replaces the package sleep with a no-op for the duration of a test.
func noSleep(t *testing.T) {
	t.Helper()
	orig := sleep
	sleep = func(time.Duration) {}
	t.Cleanup(func() { sleep = orig })
}

func TestHandleWakeUpHandshake_Success(t *testing.T) {
	tests := []struct {
		name          string
		handshake     []byte // bytes the ECU sends before the challenge
		kw2           byte
		wantChallenge byte
	}{
		{"clean frame", []byte{0x55, 0x12, 0x80}, 0x80, 0x7F},
		{"leading zeros skipped", []byte{0x00, 0x00, 0x55, 0x12, 0x80}, 0x80, 0x7F},
		{"kw2 0x83 yields 0x7C", []byte{0x55, 0x12, 0x83}, 0x83, 0x7C},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			noSleep(t)
			fake := serialtest.NewFakePort()
			fake.Enqueue(tt.handshake...)
			// echo of the challenge, then 0xE9 acknowledgement
			fake.Enqueue(tt.wantChallenge, 0xE9)

			m := newHandler(fake)
			if err := m.handleWakeUpHandshake(); err != nil {
				t.Fatalf("handshake returned error: %v", err)
			}
			if len(fake.Written) != 1 || fake.Written[0] != tt.wantChallenge {
				t.Fatalf("challenge written = %v, want [%02X]", fake.Written, tt.wantChallenge)
			}
		})
	}
}

func TestHandleWakeUpHandshake_ReadError(t *testing.T) {
	noSleep(t)
	wantErr := errors.New("boom")
	fake := serialtest.NewFakePort().EnqueueErr(wantErr)

	m := newHandler(fake)
	if err := m.handleWakeUpHandshake(); !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want %v", err, wantErr)
	}
}

func TestWaitForChallengeEcho(t *testing.T) {
	tests := []struct {
		name     string
		expected byte
		read     []byte
		wantErr  bool
	}{
		{"with tx echo", 0x7F, []byte{0x7F, 0xE9}, false},
		{"no tx echo", 0x7F, []byte{0xE9}, false},
		{"leading zeros then echo", 0x7F, []byte{0x00, 0x00, 0xE9}, false},
		{"wrong byte", 0x7F, []byte{0xAA}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := serialtest.NewFakePort()
			fake.Enqueue(tt.read...)

			m := newHandler(fake)
			err := m.waitForChallengeEcho(tt.expected)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestWaitForChallengeEcho_ReadError(t *testing.T) {
	wantErr := errors.New("io fail")
	fake := serialtest.NewFakePort().EnqueueErr(wantErr)

	m := newHandler(fake)
	if err := m.waitForChallengeEcho(0x7F); !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want %v", err, wantErr)
	}
}

func TestSend5BaudWakeup_BreakSequence(t *testing.T) {
	noSleep(t)
	fake := serialtest.NewFakePort()
	m := newHandler(fake)

	m.send5BaudWakeup()

	// Address 0x16 = 0b00010110, sent LSB-first, framed by a start bit (break
	// on) and a stop bit (break off), preceded by the idle (break off) state.
	// SetBreak(true) for logic 0, SetBreak(false) for logic 1.
	want := []bool{
		false, // idle
		true,  // start bit
		true,  // bit0 = 0
		false, // bit1 = 1
		false, // bit2 = 1
		true,  // bit3 = 0
		false, // bit4 = 1
		true,  // bit5 = 0
		true,  // bit6 = 0
		true,  // bit7 = 0
		false, // stop bit
	}
	if !slices.Equal(fake.Breaks, want) {
		t.Errorf("break sequence:\n got %v\nwant %v", fake.Breaks, want)
	}
}

func TestFlushInput_DrainsUntilEmpty(t *testing.T) {
	fake := serialtest.NewFakePort()
	fake.Enqueue(0x01, 0x02).Enqueue(0x03)
	// queue then exhausts and returns a timeout (n == 0), ending the drain.

	m := newHandler(fake)
	m.flushInput() // must return, not hang

	if len(fake.Written) != 0 {
		t.Errorf("flushInput should not write, wrote %v", fake.Written)
	}
}

func TestConnect_Success(t *testing.T) {
	noSleep(t)
	fake := serialtest.NewFakePort()
	// flushInput: immediate timeout so it does not consume the handshake bytes.
	fake.EnqueueErr(serialtest.NewTimeoutError())
	// keyword frame, then the challenge echo + 0xE9.
	fake.Enqueue(0x55, 0x12, 0x80)
	fake.Enqueue(0x7F, 0xE9)

	orig := openPort
	openPort = func(string) (sers.SerialPort, error) { return fake, nil }
	t.Cleanup(func() { openPort = orig })

	m, err := NewMEMS19(ecu.NewState(), ecu.Config{})
	if err != nil {
		t.Fatalf("NewMEMS19: %v", err)
	}
	if err := m.Connect(context.Background(), "/dev/fake"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if fake.Closed {
		t.Error("port closed after a successful Connect")
	}
}

func TestConnect_OpenError(t *testing.T) {
	wantErr := errors.New("no such port")
	orig := openPort
	openPort = func(string) (sers.SerialPort, error) { return nil, wantErr }
	t.Cleanup(func() { openPort = orig })

	m, _ := NewMEMS19(ecu.NewState(), ecu.Config{})
	if err := m.Connect(context.Background(), "/dev/missing"); !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want wrapped %v", err, wantErr)
	}
}
