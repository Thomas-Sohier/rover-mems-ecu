package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"rover-mems-agent/internal/ecu"
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

var (
	state    *ecu.State
	httpPort string
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	state = ecu.NewState()
	parseFlags()
	initializeAgent()
	go web.NewServer(state).Run(ctx, httpPort)
	runEventLoop(ctx)
}

func parseFlags() {
	serialPortFlag := flag.String("serialport", "", "Serial port to use")
	ecuTypeFlag := flag.String("ecutype", "", "ECU type to use (1.x, 1.9, 2J, rc5, 3, fake)")
	modeFlag := flag.String("mode", "prod", "Operation mode: prod or debug")
	portFlag := flag.Int("port", 8080, "HTTP server port")
	flag.Parse()

	if *serialPortFlag != "" {
		state.SelectedSerialPort = *serialPortFlag
	}
	if *ecuTypeFlag != "" {
		state.EcuType = *ecuTypeFlag
	}
	if *modeFlag == "debug" {
		state.DebugMode = true
	}
	httpPort = fmt.Sprintf(":%d", *portFlag)
}

func initializeAgent() {
	state.LogDebug("################################################################################")
	state.LogDebug("# Rover MEMS Diagnostic Agent version " + state.AgentVersion)
	state.LogDebug("################################################################################")
	state.LogDebug("Debug mode enabled")
	state.LogDebug("Selected serial port: " + state.SelectedSerialPort)
	state.LogDebug("Selected ECU type: " + state.EcuType)
}

func runEventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			state.LogDebug("Shutting down...")
			return
		default:
		}

		attemptConnection(ctx)

		select {
		case <-ctx.Done():
			state.LogDebug("Shutting down...")
			return
		case <-time.After(1 * time.Second):
		}
	}
}

func attemptConnection(ctx context.Context) {
	err := connectLoop(ctx)
	if err != nil {
		state.LogDebug(err.Error())
		state.Lock()
		state.Error = err.Error()
		state.Unlock()
	}
}

func connectLoop(ctx context.Context) error {
	state.Lock()
	state.Connected = false
	ecuType := state.EcuType
	state.Unlock()

	if ecuType == "" {
		return errors.New("no ECU type selected")
	}

	// Fake mode: skip serial port logic
	if ecuType == "fake" {
		return runECU(ctx, ecuType, ecuType)
	}

	portList, err := serial.GetPortsList()
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
		found := false
		for _, p := range portList {
			if p == selected {
				found = true
				break
			}
		}
		if !found {
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

	return runECU(ctx, ecuType, portname)
}

func runECU(ctx context.Context, ecuType, portname string) error {
	cfg := ecu.Config{
		DebugMode: state.DebugMode,
	}

	ecuInstance, err := ecu.Factory(ecuType, state, cfg)
	if err != nil {
		return err
	}
	defer ecuInstance.Close()

	if err := ecuInstance.Connect(ctx, portname); err != nil {
		return err
	}

	return ecuInstance.ReadData(ctx)
}
