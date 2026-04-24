// Package ecu defines the ECU interface and factory for different ECU types.
package ecu

import (
	"errors"
	"sync"
)

// State holds all runtime ECU data that the web server needs to read.
type State struct {
	mu sync.RWMutex

	Connected   bool
	Faults      []string
	Data        map[string]float32
	Alert       string
	Error       string
	UserCommand string
}

// NewState returns an initialized State.
func NewState() *State {
	return &State{
		Faults: []string{"not-checked-yet"},
		Data:   make(map[string]float32),
	}
}

// Lock acquires the write lock.
func (s *State) Lock()   { s.mu.Lock() }
func (s *State) Unlock() { s.mu.Unlock() }

// RLock acquires the read lock.
func (s *State) RLock()   { s.mu.RLock() }
func (s *State) RUnlock() { s.mu.RUnlock() }

// ECU is the interface that all ECU implementations must satisfy.
type ECU interface {
	// Connect establishes communication with the ECU via the given serial port.
	// It performs any required wake-up sequence and initialization handshake.
	Connect(portName string) error

	// ReadData runs the main data loop, continuously reading from the ECU
	// and updating the shared State. It blocks until an error occurs or
	// the connection is closed.
	ReadData() error

	// GetFaults returns the current list of fault codes.
	GetFaults() []string

	// SendCommand queues a user command to be sent on the next loop iteration.
	SendCommand(cmd string) error

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
