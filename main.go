package main

// useful: https://github.com/bugst/go-serial/blob/master/serial_windows.go

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var (
	globalConnected          = false
	globalFaults             = []string{"not-checked-yet"}
	globalSerialPorts        = []string{}
	globalSelectedSerialPort = ""
	globalEcuType            = ""
	globalUserCommand        = ""
	globalAlert              = "" // pops up on web UI then closes itself
	globalError              = "" // pops up on web UI and stays until closed
	globalDebugMode          = false
	globalHTTPPort           = ":8080"

	globalDataOutput     = map[string]float32{}
	globalDataOutputLock = sync.RWMutex{}

	globalAgentVersion = "1.4.3"

	globalLogLines = []string{}

	outgoingData chan string // for pushing data out of the websocket

	serialReadChannel = make(chan byte, 1024)
)

// main is the entry point of the application.
// It handles flag parsing, agent initialization, signal handling setup, and the main event loop.
func main() {
	parseFlags()
	initializeAgent()
	go runWebserver(globalHTTPPort)
	stopChan := setupGracefulShutdown()
	runEventLoop(stopChan)
}

// parseFlags parses command-line arguments to configure the agent.
func parseFlags() {
	serialPortFlag := flag.String("serialport", "", "Serial port to use")
	ecuTypeFlag := flag.String("ecutype", "", "ECU type to use (1.x, 1.9, 2J, rc5, 3)")
	modeFlag := flag.String("mode", "prod", "Operation mode: prod or debug")
	httpPortFlag := flag.String("httpport", ":8080", "HTTP server bind address (e.g. :8080 or 0.0.0.0:9090)")
	flag.Parse()

	if *serialPortFlag != "" {
		globalSelectedSerialPort = *serialPortFlag
	}
	if *ecuTypeFlag != "" {
		globalEcuType = *ecuTypeFlag
	}
	if *modeFlag == "debug" {
		globalDebugMode = true
	}
	globalHTTPPort = *httpPortFlag
}

// initializeAgent sets up initial state, logging, and data channels.
func initializeAgent() {
	outgoingData = make(chan string, 1000)
	fmt.Println("################################################################################")
	fmt.Println("# Rover MEMS Diagnostic Agent version " + globalAgentVersion)
	fmt.Println("################################################################################")
	logDebug("Debug mode enabled")
	logDebug("Selected serial port: " + globalSelectedSerialPort)
	logDebug("Selected ECU type: " + globalEcuType)
}

// setupGracefulShutdown configures a channel to receive OS signals for termination.
// It returns a channel that will receive os.Interrupt or syscall.SIGTERM signals.
func setupGracefulShutdown() chan os.Signal {
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
	return stopChan
}

// runEventLoop manages the main application lifecycle.
// It attempts to connect to the ECU, then waits 1 second before retrying on failure.
// Signal handling is checked between each attempt.
func runEventLoop(stopChan chan os.Signal) {
	for {
		// Check for shutdown before each attempt
		select {
		case <-stopChan:
			logDebug("Shutting down...")
			return
		default:
		}

		attemptConnection()

		// Wait 1 second before retrying, but honour shutdown immediately
		select {
		case <-stopChan:
			logDebug("Shutting down...")
			return
		case <-time.After(1 * time.Second):
		}
	}
}

// attemptConnection tries to establish a connection to the ECU.
// If it fails, it logs the error and updates the global error state.
func attemptConnection() {
	err := connectLoop()
	if err != nil {
		logDebug(err)
		globalDataOutputLock.Lock()
		globalError = err.Error()
		globalDataOutputLock.Unlock()
	}
}

// connectLoop handles the logic for discovering serial ports and connecting to the specific ECU type.
// It returns an error if connection fails or if configuration is invalid, nil on clean exit.
func connectLoop() error {
	// Mark as disconnected at the start of each attempt so the UI reflects reality
	globalDataOutputLock.Lock()
	globalConnected = false
	globalDataOutputLock.Unlock()

	if globalEcuType == "" {
		return errors.New("No ECU type selected")
	}

	portList, err := nativeGetPortsList()
	if err != nil {
		return err
	}

	// Case: No ports found at all
	if len(portList) == 0 {
		logDebug("ERROR: No serial ports found. Please check device connections and drivers.")
		os.Exit(1)
	}

	globalDataOutputLock.Lock()
	globalSerialPorts = portList
	globalDataOutputLock.Unlock()

	logDebug("Found the following ports that I can use:")
	logDebug(portList)

	portname := ""

	// Determine which port to use
	globalDataOutputLock.Lock()
	selected := globalSelectedSerialPort
	globalDataOutputLock.Unlock()

	if selected != "" {
		// User has selected a port
		found := false
		for _, p := range portList {
			if p == selected {
				found = true
				break
			}
		}

		if !found {
			// Warn once per "session" implies we assume the user knows what they are doing,
			// or the port is hidden. We log a warning and try anyway.
			fmt.Printf("WARNING: Selected port '%s' not found in discovered list. Attempting to connect anyway...\n", selected)
		}
		portname = selected
	} else {
		// No port selected
		if len(portList) == 1 {
			portname = portList[0]
			logDebug("Only found one port, auto-selecting: " + portname)

			globalDataOutputLock.Lock()
			globalSelectedSerialPort = portname
			globalDataOutputLock.Unlock()
		} else {
			// Multiple ports and none selected
			// User requested: "if not set, log warn and then take first"
			portname = portList[0]
			fmt.Printf("WARNING: Multiple ports found and none selected. Defaulting to first available: %s\n", portname)

			globalDataOutputLock.Lock()
			globalSelectedSerialPort = portname
			globalDataOutputLock.Unlock()
		}
	}

	logDebug("Using port: " + portname)

	switch globalEcuType {
	case "1.x":
		_, err = readFirstBytesFromPortEcu1x(portname)
	case "rc5":
		_, err = readFirstBytesFromPortRc5(portname)
	case "2J":
		_, err = readFirstBytesFromPortTwoj(portname)
	case "1.9":
		_, err = readFirstBytesFromPortEcu19(portname)
	case "3":
		_, err = readFirstBytesFromPortEcu3(portname)
	default:
		return errors.New("Unknown ECU type set")
	}
	if err != nil {
		return err
	}

	return nil
}
