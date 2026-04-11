package main

import (
	"errors"
	"time"

	"github.com/distributed/sers"
)

var (
	ecu1xInitCommand = []byte{0xCA, 0x75, 0xD0}
)

func readFirstBytesFromPortEcu19(fn string) ([]byte, error) {
	logDebug("Connecting to MEMS 1.9 ECU")

	sp, err := sers.Open(fn)
	if err != nil {
		return nil, err
	}
	defer sp.Close()

	err = sp.SetMode(9600, 8, sers.N, 1, sers.NO_HANDSHAKE)
	if err != nil {
		return nil, err
	}

	sp.SetReadParams(1, 0.5)
	flushInput(sp)

	sp.SetBreak(false)
	time.Sleep(500 * time.Millisecond)

	send5BaudWakeup(sp)

	err = handleWakeUpHandshake(sp)
	if err != nil {
		return nil, err
	}

	// Send 1.3/1.6 init sequence
	logDebug("Sending standard Init (0xCA, 0x75, 0xD0)")
	sp.SetReadParams(1, 2.0)

	_, err = sp.Write(ecu1xInitCommand)
	if err != nil {
		return nil, err
	}

	// Wait for echo + 4 byte ECU ID
	ecuID, err := readInitResponse(sp)
	if err != nil {
		return nil, err
	}

	logDebugf("ECU ID: %x", ecuID)

	return ecu1xLoop(sp, true)
}

func handleWakeUpHandshake(sp sers.SerialPort) error {
	buffer := make([]byte, 0)
	tmp := make([]byte, 128)

	start := time.Now()
	for time.Since(start) < 2500*time.Millisecond {
		n, err := sp.Read(tmp)
		if err != nil {
			return err
		}
		if n > 0 {
			buffer = append(buffer, tmp[:n]...)
			logDebug(buffer)

			// Remove leading zeros
			for len(buffer) > 0 && buffer[0] == 0x00 {
				buffer = buffer[1:]
			}

			// Check for wake response: 55 76 83
			if len(buffer) >= 3 && buffer[0] == 0x55 && buffer[1] == 0x76 && buffer[2] == 0x83 {
				logDebug("1.9 ECU Woke Response received (55 76 83)")

				time.Sleep(25 * time.Millisecond)

				// Send inverted second byte (0x83 ^ 0xFF = 0x7C)
				challengeResponse := buffer[2] ^ 0xFF
				logDebugf("Sending Challenge Response: 0x%02X", challengeResponse)
				_, err = sp.Write([]byte{challengeResponse})
				if err != nil {
					return err
				}

				// Wait for echo + 0xE9
				return waitForChallengeEcho(sp, challengeResponse)
			}
		}
	}
	return errors.New("timeout waiting for 55 76 83")
}

func waitForChallengeEcho(sp sers.SerialPort, expectedEcho byte) error {
	buffer := make([]byte, 0)
	tmp := make([]byte, 128)

	start := time.Now()
	for time.Since(start) < 1000*time.Millisecond {
		n, err := sp.Read(tmp)
		if err != nil {
			return err
		}
		if n > 0 {
			buffer = append(buffer, tmp[:n]...)

			// Wait for echo + 0xE9
			if len(buffer) >= 2 && buffer[0] == expectedEcho && buffer[1] == 0xE9 {
				logDebug("1.9 ECU init handshake complete")
				return nil
			}
		}
	}
	return errors.New("timeout waiting for challenge echo")
}

func readInitResponse(sp sers.SerialPort) ([]byte, error) {
	buffer := make([]byte, 0)
	tmp := make([]byte, 128)
	expectedLength := 7 // 0xCA, 0x75, 0xD0 + 4 bytes ECU ID

	start := time.Now()
	for time.Since(start) < 2000*time.Millisecond {
		n, err := sp.Read(tmp)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			buffer = append(buffer, tmp[:n]...)

			if len(buffer) >= expectedLength {
				// Verify echo
				if buffer[0] != 0xCA || buffer[1] != 0x75 || buffer[2] != 0xD0 {
					return nil, errors.New("invalid init response echo")
				}
				// Return 4-byte ECU ID
				return buffer[3:7], nil
			}
		}
	}
	return nil, errors.New("timeout reading init response")
}

func flushInput(sp sers.SerialPort) {
	buf := make([]byte, 1024)
	for {
		n, _ := sp.Read(buf)
		if n == 0 {
			break
		}
	}
}

func send5BaudWakeup(sp sers.SerialPort) {
	buffer := make([]byte, 128)
	for {
		n, _ := sp.Read(buffer)
		if n == 0 {
			break
		}
	}

	// Idle High
	sp.SetBreak(false)
	time.Sleep(500 * time.Millisecond)

	// Start bit
	sp.SetBreak(true)
	time.Sleep(200 * time.Millisecond)

	// Data bits (0x16 LSB first)
	ecuAddress := 0x16
	for i := 0; i < 8; i++ {
		bit := (ecuAddress >> i) & 1
		if bit > 0 {
			sp.SetBreak(false)
		} else {
			sp.SetBreak(true)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Stop bit
	sp.SetBreak(false)
	time.Sleep(200 * time.Millisecond)
}
