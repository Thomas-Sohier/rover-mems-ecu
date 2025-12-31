package main

// useful: https://github.com/bugst/go-serial/blob/master/serial_windows.go

import (
	"errors"
	"flag"
	"fmt"
	"sync"
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

	globalDataOutput     = map[string]float32{}
	globalDataOutputLock = sync.RWMutex{}

	globalAgentVersion = "1.4.3"

	globalLogLines = []string{}

	outgoingData chan string // for pushing data out of the websocket

	serialReadChannel = make(chan byte, 1024)
)

func main() {
	serialPortFlag := flag.String("serialport", "", "Serial port to use")
	ecuTypeFlag := flag.String("ecutype", "", "ECU type to use")
	modeFlag := flag.String("mode", "prod", "Operation mode: prod or debug")
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

	outgoingData = make(chan string, 1000)
	fmt.Println("################################################################################")
	fmt.Println("# Rover MEMS Diagnostic Agent version " + globalAgentVersion)
	fmt.Println("################################################################################")
	logDebug("Debug mode enabled")
	logDebug("Selected serial port: " + globalSelectedSerialPort)
	logDebug("Selected ECU type: " + globalEcuType)

	go runWebserver()

	for {
		err := connectLoop()
		if err != nil {
			logDebug(err)
			globalDataOutputLock.Lock()
			globalError = err.Error()
			globalDataOutputLock.Unlock()
		}

		time.Sleep(1 * time.Second)
	}

}

func connectLoop() error {

	if globalEcuType == "" {
		// return nil
		return errors.New("No ECU type selected")
	}

	portList, err := nativeGetPortsList()
	if err != nil {
		return err
	}
	if len(portList) > 0 {
		logDebug("Found the following ports that I can use:")
		logDebug(portList)

	}

	globalDataOutputLock.Lock()
	globalSerialPorts = portList
	globalDataOutputLock.Unlock()

	portname := ""

	if len(portList) == 1 {
		logDebug("Only found one port so I'm going to use it")

		portname = portList[0]

		globalDataOutputLock.Lock()
		globalSelectedSerialPort = portname
		globalDataOutputLock.Unlock()

	} else if len(portList) > 1 {
		globalDataOutputLock.Lock()
		if globalSelectedSerialPort == "" {
			globalDataOutputLock.Unlock()
			return errors.New("Multiple COM ports found, select one")
		} else {
			portname = globalSelectedSerialPort
		}
		globalDataOutputLock.Unlock()
	} else {
		return errors.New("No serial ports found, check device manager, do you need to install a driver?")
	}

	logDebug("Using port:")
	logDebug(portname)

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

	return errors.New("Connect loop finished")

}
