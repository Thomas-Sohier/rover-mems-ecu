// Package ecu defines the ECU interface and factory for different ECU types.
package ecu

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
)

// maxLogLines is the number of debug lines retained in the ring of LogLines.
const maxLogLines = 100

// State holds all runtime ECU data shared between ECU handlers and the web server.
type State struct {
	// mu guards the ECU data fields below. It is deliberately NOT the mutex that
	// guards LogLines: ECU parsers log while holding mu, and sync.RWMutex is not
	// reentrant, so sharing one mutex would self-deadlock every debug-mode run.
	mu sync.RWMutex

	// logMu guards LogLines only. Lock ordering is mu -> logMu (Snapshot takes
	// both); nothing ever takes mu while holding logMu.
	logMu sync.Mutex

	// ECU connection state
	Connected   bool
	Faults      []string
	Data        map[string]float32
	Alert       string
	Error       string
	UserCommand string

	// Runtime configuration (can be changed via web UI)
	EcuType            string
	SelectedSerialPort string
	SerialPorts        []string

	// Static configuration
	DebugMode    bool
	AgentVersion string

	// Logging
	LogLines []string
}

// NewState returns an initialized State.
func NewState() *State {
	return &State{
		Faults:       []string{"not-checked-yet"},
		Data:         make(map[string]float32),
		SerialPorts:  []string{},
		LogLines:     []string{},
		AgentVersion: "1.4.3",
	}
}

// LogDebug appends a debug message to LogLines if DebugMode is enabled.
// It is safe to call while holding the State lock.
func (s *State) LogDebug(args ...any) {
	if !s.DebugMode {
		return
	}
	s.appendLog(fmt.Sprint(args...))
}

// LogDebugf appends a formatted debug message.
// It is safe to call while holding the State lock.
func (s *State) LogDebugf(format string, args ...any) {
	if !s.DebugMode {
		return
	}
	s.appendLog(fmt.Sprintf(format, args...))
}

// appendLog prints msg and pushes it onto the bounded LogLines ring.
func (s *State) appendLog(msg string) {
	fmt.Println(msg)
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.LogLines = append(s.LogLines, msg)
	if len(s.LogLines) > maxLogLines {
		s.LogLines = s.LogLines[len(s.LogLines)-maxLogLines:]
	}
}

// LogLinesCopy returns a copy of the retained debug lines.
func (s *State) LogLinesCopy() []string {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	return slices.Clone(s.LogLines)
}

// Lock acquires the write lock.
func (s *State) Lock()   { s.mu.Lock() }
func (s *State) Unlock() { s.mu.Unlock() }

// RLock acquires the read lock.
func (s *State) RLock()   { s.mu.RLock() }
func (s *State) RUnlock() { s.mu.RUnlock() }

// Snapshot is a consistent, copied view of State for read-only consumers
// (the web layer). Slices and the map are deep-copied so callers can use
// them without holding the lock.
type Snapshot struct {
	Connected          bool
	Faults             []string
	Data               map[string]float32
	Alert              string
	Error              string
	UserCommand        string
	EcuType            string
	SelectedSerialPort string
	SerialPorts        []string
	AgentVersion       string
	LogLines           []string
}

// Snapshot returns a consistent, copied view of the State under the read lock.
// It does not mutate the State (in particular it does not consume Alert/Error).
func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Snapshot{
		Connected:          s.Connected,
		Faults:             slices.Clone(s.Faults),
		Data:               maps.Clone(s.Data),
		Alert:              s.Alert,
		Error:              s.Error,
		UserCommand:        s.UserCommand,
		EcuType:            s.EcuType,
		SelectedSerialPort: s.SelectedSerialPort,
		SerialPorts:        slices.Clone(s.SerialPorts),
		AgentVersion:       s.AgentVersion,
		LogLines:           s.LogLinesCopy(),
	}
}

// ConsumeAlertError reads and clears the Alert and Error fields, returning their
// previous values. Alerts and errors are one-shot: they are reported once to the
// consumer and consuming them clears them so they are not reported again.
func (s *State) ConsumeAlertError() (alert, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	alert, errMsg = s.Alert, s.Error
	s.Alert = ""
	s.Error = ""
	return alert, errMsg
}

// SetEcuType sets the selected ECU type.
func (s *State) SetEcuType(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.EcuType = v
}

// SetSelectedSerialPort sets the selected serial port.
func (s *State) SetSelectedSerialPort(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SelectedSerialPort = v
}

// SetUserCommand sets the pending user command.
func (s *State) SetUserCommand(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.UserCommand = v
}

// ECU is the interface that all ECU implementations must satisfy.
type ECU interface {
	// Connect establishes communication with the ECU via the given serial port.
	// It performs any required wake-up sequence and initialization handshake.
	// It returns ctx.Err() if the context is cancelled before the handshake completes.
	Connect(ctx context.Context, portName string) error

	// ReadData runs the main data loop, continuously reading from the ECU
	// and updating the shared State. It blocks until an error occurs,
	// the connection is closed, or ctx is cancelled (in which case it returns ctx.Err()).
	ReadData(ctx context.Context) error

	// Close terminates the connection and releases resources.
	Close() error

	// Type returns the ECU type identifier (e.g., "1.x", "2J").
	Type() string
}

// Config holds the configuration needed to create an ECU instance.
type Config struct {
	DebugMode bool
}

// Registry holds constructor functions for each ECU type.
// Implementations register themselves via Register().
var registry = make(map[string]Constructor)

// Constructor is a function that creates an ECU instance.
type Constructor func(state *State, cfg Config) (ECU, error)

// Register adds an ECU constructor to the registry.
// Called by each ECU implementation's init() function.
func Register(ecuType string, ctor Constructor) {
	registry[ecuType] = ctor
}

// Factory creates an ECU instance based on the ecuType string.
// The state parameter is the shared state that the ECU will update.
func Factory(ecuType string, state *State, cfg Config) (ECU, error) {
	ctor, ok := registry[ecuType]
	if !ok {
		return nil, errors.New("unknown ECU type: " + ecuType)
	}
	return ctor(state, cfg)
}

// SupportedTypes returns a list of all registered ECU types.
func SupportedTypes() []string {
	types := make([]string, 0, len(registry))
	for t := range registry {
		types = append(types, t)
	}
	return types
}
