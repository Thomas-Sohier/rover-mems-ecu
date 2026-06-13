package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"rover-mems-agent/internal/ble"
	"rover-mems-agent/internal/bluetooth"
	"rover-mems-agent/internal/ecu"
	"rover-mems-agent/internal/nowplaying"
	"rover-mems-agent/internal/serial"
	"rover-mems-agent/internal/web"

	// Import ECU implementations for their init() registration
	_ "rover-mems-agent/internal/ecu/fake"
	_ "rover-mems-agent/internal/ecu/mems19"
	_ "rover-mems-agent/internal/ecu/mems1x"
	_ "rover-mems-agent/internal/ecu/mems2j"
	_ "rover-mems-agent/internal/ecu/mems3"
	_ "rover-mems-agent/internal/ecu/rc5"
)

// getPorts lists the available serial ports. It is a package variable so tests
// can inject a deterministic port list instead of enumerating real hardware.
var getPorts = serial.GetPortsList

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	state := ecu.NewState()
	npStore := nowplaying.NewStore()
	httpPort, bleName, bleEnabled := parseFlags(state, os.Args[1:])
	initializeAgent(state)
	if err := bluetooth.SetupAgent(); err != nil {
		log.Printf("bluetooth: setup agent: %v", err)
	}
	if bleEnabled {
		go func() {
			if err := ble.Run(ctx, npStore, bleName); err != nil {
				log.Printf("ble: %v", err)
			}
		}()
	}
	go web.NewServer(state, npStore).Run(ctx, httpPort)
	runEventLoop(ctx, state)
}

func parseFlags(state *ecu.State, args []string) (httpPort, bleName string, bleEnabled bool) {
	fs := flag.NewFlagSet("rover-mems", flag.ExitOnError)
	serialPortFlag := fs.String("serialport", "", "Serial port to use")
	ecuTypeFlag := fs.String("ecutype", "", "ECU type to use (1.x, 1.9, 2J, rc5, 3, fake)")
	modeFlag := fs.String("mode", "prod", "Operation mode: prod or debug")
	portFlag := fs.Int("port", 8080, "HTTP server port")
	bleFlag := fs.Bool("ble", true, "Enable BLE GATT peripheral for companion phone app")
	bleNameFlag := fs.String("blename", "Rover MEMS", "BLE local device name for advertising")
	_ = fs.Parse(args) // flag.ExitOnError: Parse never returns an error here

	if *serialPortFlag != "" {
		state.SelectedSerialPort = *serialPortFlag
	}
	if *ecuTypeFlag != "" {
		state.EcuType = *ecuTypeFlag
	}
	if *modeFlag == "debug" {
		state.DebugMode = true
	}
	return fmt.Sprintf(":%d", *portFlag), *bleNameFlag, *bleFlag
}

func initializeAgent(state *ecu.State) {
	state.LogDebug("################################################################################")
	state.LogDebug("# Rover MEMS Diagnostic Agent version " + state.AgentVersion)
	state.LogDebug("################################################################################")
	state.LogDebug("Debug mode enabled")
	state.LogDebug("Selected serial port: " + state.SelectedSerialPort)
	state.LogDebug("Selected ECU type: " + state.EcuType)
}

func runEventLoop(ctx context.Context, state *ecu.State) {
	for {
		select {
		case <-ctx.Done():
			state.LogDebug("Shutting down...")
			return
		default:
		}

		attemptConnection(ctx, state)

		select {
		case <-ctx.Done():
			state.LogDebug("Shutting down...")
			return
		case <-time.After(1 * time.Second):
		}
	}
}

func attemptConnection(ctx context.Context, state *ecu.State) {
	err := connectLoop(ctx, state)
	if err != nil {
		state.LogDebug(err.Error())
		state.Lock()
		state.Error = err.Error()
		state.Unlock()
	}
}

func connectLoop(ctx context.Context, state *ecu.State) error {
	state.Lock()
	state.Connected = false
	ecuType := state.EcuType
	state.Unlock()

	if ecuType == "" {
		return errors.New("no ECU type selected")
	}

	state.LogDebugf("connectLoop: starting connection attempt for ECU type %q", ecuType)

	// Fake mode: skip serial port logic
	if ecuType == "fake" {
		state.LogDebug("connectLoop: fake mode, skipping serial port discovery")
		return runECU(ctx, state, ecuType, ecuType)
	}

	portList, err := getPorts()
	if err != nil {
		return err
	}

	if len(portList) == 0 {
		state.LogDebug("ERROR: No serial ports found. Please check device connections and drivers.")
		return errors.New("no serial ports found")
	}

	state.Lock()
	state.SerialPorts = portList
	selected := state.SelectedSerialPort
	state.Unlock()

	state.LogDebug("Found ports:", portList)

	portname := ""

	if selected != "" {
		if !slices.Contains(portList, selected) {
			state.LogDebugf("WARNING: Selected port '%s' not found in discovered list. Attempting to connect anyway...", selected)
		}
		portname = selected
	} else {
		portname = portList[0]
		if len(portList) == 1 {
			state.LogDebug("Only found one port, auto-selecting: " + portname)
		} else {
			state.LogDebugf("WARNING: Multiple ports found and none selected. Defaulting to first available: %s", portname)
		}
		state.Lock()
		state.SelectedSerialPort = portname
		state.Unlock()
	}

	state.LogDebug("Using port: " + portname)

	return runECU(ctx, state, ecuType, portname)
}

func runECU(ctx context.Context, state *ecu.State, ecuType, portname string) error {
	cfg := ecu.Config{
		DebugMode: state.DebugMode,
	}

	ecuInstance, err := ecu.Factory(ecuType, state, cfg)
	if err != nil {
		return fmt.Errorf("create ECU handler for type %q: %w", ecuType, err)
	}
	defer ecuInstance.Close()

	state.LogDebugf("runECU: connecting %s handler on port %s", ecuType, portname)
	if err := ecuInstance.Connect(ctx, portname); err != nil {
		return fmt.Errorf("connect %s on %s: %w", ecuType, portname, err)
	}

	state.LogDebugf("runECU: connected, starting data read loop for %s", ecuType)
	return ecuInstance.ReadData(ctx)
}
