package main

import (
	"errors"
	"time"

	"github.com/distributed/sers"
)

var (
	ecu19SpecificInitCommand = []byte{0x7C}

	ecu19WokeResponse         = []byte{0x55, 0x76, 0x83}
	ecu19SpecificInitResponse = []byte{ecu19SpecificInitCommand[0], 0xE9} // includes our echo
)

func readFirstBytesFromPortEcu19(fn string) ([]byte, error) {

	logDebug("Connecting to MEMS 1.9 ECU")

	globalConnected = false

	sp, err := sers.Open(fn)
	if err != nil {
		return nil, err
	}
	defer sp.Close()

	// 9600 baud is standard for initial connection attempts
	err = sp.SetMode(9600, 8, sers.N, 1, sers.NO_HANDSHAKE)
	if err != nil {
		return nil, err
	}

	// setting:
	// minread = 0: minimal buffering on read, return characters as early as possible
	// timeout = 1.0: time out if after 1.0 seconds nothing is received
	err = sp.SetReadParams(0, 0.001)
	if err != nil {
		return nil, err
	}

	mode, err := sp.GetMode()
	logDebug("Serial cable set to:")
	logDebug(mode)

	// Try the standard ECU 1.x connection method first (fast check).
	// Sometimes the ECU might be already awake or in a compatible state.
	if testEcu1xConnection(sp) {
		logDebug("ECU 1.x is already awake, entering main loop")
		return ecu1xLoop(sp, true)
	}

	// If standard loop returns, it means we need to perform the specific initialization.
	// Reset the line state first.
	sp.SetBreak(false)
	time.Sleep(2000 * time.Millisecond)

	// Perform the 5-baud init sequence to wake up the ECU.
	// This involves manually bit-banging the 0x16 byte at a very slow speed.
	send5BaudWakeup(sp)
	return handleEcu19ResponseLoop(sp)
}

// send5BaudWakeup performs the slow initialization sequence required by MEMS 1.9 ECUs.
// It effectively simulates a 5-baud transmission of the byte 0x16 by manually controlling the break state.
func send5BaudWakeup(sp sers.SerialPort) {
	start := time.Now()

	// Start bit (Break condition)
	sp.SetBreak(true)
	sleepUntil(start, 200)

	// Send the byte 0x16 (0001 0110 in binary) LSB first
	ecuAddress := 0x16
	for i := 0; i < 8; i++ {
		bit := (ecuAddress >> i) & 1
		if bit > 0 {
			sp.SetBreak(false) // Logic 1 (Mark)
		} else {
			sp.SetBreak(true) // Logic 0 (Space/Break)
		}
		// Each bit takes 200ms (1000ms / 5 baud = 200ms)
		sleepUntil(start, 200+((i+1)*200))
	}

	// Stop bit (Mark condition)
	sp.SetBreak(false)
	sleepUntil(start, 200+(8*200)+200)
}

// handleEcu19ResponseLoop listens for the ECU's wake-up response and handles the authentication handshake.
func handleEcu19ResponseLoop(sp sers.SerialPort) ([]byte, error) {
	// Set a 2 second timeout for reading properly from the ECU
	// This avoids the need for manual sleep loops
	err := sp.SetReadParams(1, 20.0)
	if err != nil {
		return nil, err
	}

	buffer := make([]byte, 0)

	// We'll try to read a few times to gather the full response if it's fragmented,
	// but the native timeout will handle the "no response" case.
	// We loop strictly to accumulate data until we have a valid packet or fail.
	for {
		// Read from serial port (blocks up to 2s)
		rb := make([]byte, 128)
		n, err := sp.Read(rb[:])
		if err != nil {
			return nil, err
		}
		if n == 0 {
			// Timeout occurred (no data after 2 seconds)
			return nil, errors.New("MEMS 1.9 timed out (no response)")
		}

		buffer = append(buffer, rb[:n]...)
		logDebug(buffer)

		// Filter out leading zeros which might be artifacts from the wake-up sequence
		// (Keep this logic as it handles noise)
		for len(buffer) > 0 && buffer[0] == 0x00 {
			buffer = buffer[1:]
		}

		if len(buffer) == 0 {
			continue
		}

		// Check if we received the "Woke Response" (0x55, 0x76, 0x83)
		if slicesEqual(buffer, ecu19WokeResponse) {
			logDebug("1.9 ECU woke up - init stage 1")

			// The ECU sends a challenge byte (the 3rd byte, usually 0x83)
			// We must respond with the inverse (XOR 0xFF) of this byte.
			challengeByte := buffer[2]
			buffer = nil // Clear buffer for next read

			// Small delay before sending response, just to be safe with the ECU processing time
			time.Sleep(50 * time.Millisecond)

			// Calculate and send response
			responseByte := challengeByte ^ 0xFF
			logDebugf("Sending challenge response: %x (derived from %x)", responseByte, challengeByte)
			_, err = sp.Write([]byte{responseByte})
			if err != nil {
				return nil, err
			}
			continue
		}

		// Check if we received the "Init Stage 2" response (Echo + 0xE9)
		if slicesEqual(buffer, ecu19SpecificInitResponse) {
			logDebug("1.9 ECU init stage 2")
			// Switch back to normal non-blocking read params for the main loop if needed,
			// or let ecu1xLoop handle its own config.
			// Handshake complete, hand over to the main communication loop
			return ecu1xLoop(sp, true)
		}

		// Safety break if buffer gets too large without matching anything
		if len(buffer) > 128 {
			return nil, errors.New("garbage received from ECU 1.9")
		}
	}
}
