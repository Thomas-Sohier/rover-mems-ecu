package main

import (
	"errors"
	"time"

	"github.com/distributed/sers"
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

	// send5BaudWakeup already handles the idle-high before start bit
	send5BaudWakeup(sp)

	err = handleWakeUpHandshake(sp)
	if err != nil {
		return nil, err
	}

	// Hand off to the standard 1.x loop which sends CA/75/D0 and enters data mode
	sp.SetReadParams(0, 0.001)
	return ecu1xLoop(sp, true)
}

func handleWakeUpHandshake(sp sers.SerialPort) error {
	buffer := make([]byte, 0)
	tmp := make([]byte, 128)

	// Challenge is always 0x7C (= 0x83 ^ 0xFF) for MEMS 1.9.
	// Overridden dynamically if we actually receive KW2 from the ECU.
	challengeResponse := byte(0x7C)

	start := time.Now()
	for time.Since(start) < 2000*time.Millisecond {
		n, err := sp.Read(tmp)
		if err != nil {
			return err
		}
		if n > 0 {
			buffer = append(buffer, tmp[:n]...)
			logDebug(buffer)

			// Remove leading zeros (K-line artifact)
			for len(buffer) > 0 && buffer[0] == 0x00 {
				buffer = buffer[1:]
			}

			// Check for wake response: 0x55 0x76 0x83 (MEMS 1.9 specific)
			if len(buffer) >= 3 && buffer[0] == 0x55 {
				kw1, kw2 := buffer[1], buffer[2]
				logDebugf("1.9 ECU Woke Response received (55 %02X %02X)", kw1, kw2)
				challengeResponse = kw2 ^ 0xFF
				break
			}
		}
	}

	// Per Android app: if we timed out waiting for the wakeup response,
	// continue anyway and send the challenge — do not abort.
	if challengeResponse == 0x7C {
		logDebug("1.9 ECU: sending challenge 0x7C (default or derived from KW2=0x83)")
	}

	time.Sleep(25 * time.Millisecond)

	logDebugf("Sending Challenge Response: 0x%02X", challengeResponse)
	_, err := sp.Write([]byte{challengeResponse})
	if err != nil {
		return err
	}

	return waitForChallengeEcho(sp, challengeResponse)
}

func waitForChallengeEcho(sp sers.SerialPort, expectedEcho byte) error {
	buffer := make([]byte, 0)
	tmp := make([]byte, 128)

	start := time.Now()
	for time.Since(start) < 2000*time.Millisecond {
		n, err := sp.Read(tmp)
		if err != nil {
			return err
		}
		if n > 0 {
			buffer = append(buffer, tmp[:n]...)

			// Remove leading zeros (K-line artifact)
			for len(buffer) > 0 && buffer[0] == 0x00 {
				buffer = buffer[1:]
			}

			// Some K-line interfaces echo our own transmitted byte back; others suppress it.
			// Accept [expectedEcho, 0xE9] (with echo) or [0xE9] alone (without echo).
			if len(buffer) >= 2 && buffer[0] == expectedEcho && buffer[1] == 0xE9 {
				logDebug("1.9 ECU init handshake complete (with TX echo)")
				return nil
			}
			if len(buffer) >= 1 && buffer[0] == 0xE9 {
				logDebug("1.9 ECU init handshake complete (no TX echo)")
				return nil
			}
		}
	}
	return errors.New("timeout waiting for challenge echo (0xE9)")
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
	// Idle High before start bit (>= 300ms required by ISO 9141)
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
