package mems2j

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"rover-mems-agent/internal/ecu"
	"rover-mems-agent/internal/serial"
	"rover-mems-agent/pkg/utils"

	"github.com/distributed/sers"
)

func init() {
	ecu.Register("2J", NewMEMS2J)
}

// MEMS2J handles MEMS 2J ECUs (KV6, K-series).
type MEMS2J struct {
	mu        sync.RWMutex
	data      map[string]float32
	faults    []string
	alert     string
	connected bool
	debugMode bool

	sp              sers.SerialPort
	reader          *serial.Reader
	lastSentCommand []byte
	seed            int
	key             int
	userCommand     string
}

// NewMEMS2J creates a new MEMS 2J ECU handler.
func NewMEMS2J(_ *ecu.State, cfg ecu.Config) (ecu.ECU, error) {
	return &MEMS2J{
		data:      make(map[string]float32),
		faults:    []string{},
		reader:    serial.NewReader(),
		debugMode: cfg.DebugMode,
	}, nil
}

func (m *MEMS2J) Connect(portName string) error {
	m.logDebug("Connecting to MEMS 2J ECU")
	m.mu.Lock()
	m.connected = false
	m.mu.Unlock()

	sp, err := sers.Open(portName)
	if err != nil {
		return err
	}
	m.sp = sp

	if err = sp.SetMode(10400, 8, sers.N, 1, sers.NO_HANDSHAKE); err != nil {
		sp.Close()
		return err
	}

	if err = sp.SetReadParams(0, 0); err != nil {
		sp.Close()
		return err
	}

	mode, _ := sp.GetMode()
	m.logDebug("Serial cable set to:")
	m.logDebug(fmt.Sprint(mode))

	m.reader.Start(sp)

	if err := m.wakeUp(); err != nil {
		sp.Close()
		return err
	}

	return nil
}

func (m *MEMS2J) ReadData() error {
	return m.loop()
}

func (m *MEMS2J) GetFaults() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]string, len(m.faults))
	copy(result, m.faults)
	return result
}

func (m *MEMS2J) GetData() map[string]float32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]float32, len(m.data))
	for k, v := range m.data {
		result[k] = v
	}
	return result
}

func (m *MEMS2J) IsConnected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connected
}

func (m *MEMS2J) SendCommand(cmd string) error {
	m.mu.Lock()
	m.userCommand = cmd
	m.mu.Unlock()
	return nil
}

func (m *MEMS2J) Close() error {
	m.mu.Lock()
	m.connected = false
	m.mu.Unlock()
	if m.sp != nil {
		return m.sp.Close()
	}
	return nil
}

func (m *MEMS2J) Type() string {
	return "2J"
}

func (m *MEMS2J) logDebug(msg string) {
	if m.debugMode {
		fmt.Println(msg)
	}
}

func (m *MEMS2J) logDebugf(format string, args ...interface{}) {
	if m.debugMode {
		fmt.Printf(format+"\n", args...)
	}
}

var (
	initCommand = []byte{0x81, 0x13, 0xF7, 0x81, 0x0C}

	startDiagnostic = []byte{0x10, 0xA0}
	requestSeed     = []byte{0x27, 0x01}
	sendKey         = []byte{0x27, 0x02}
	pingCommand     = []byte{0x3E}

	clearFaultsCommand    = []byte{0x31, 0xCB, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	faultsClearedResponse = []byte{0x71, 0xCB}

	learnImmoCommand         = []byte{0x31, 0xD0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	responseLearnImmoCommand = []byte{0x71, 0xD0}

	read722Command = []byte{0x23, 0x00, 0x07, 0x22, 0x01}

	requestService13    = []byte{0x13}
	requestService31_d5 = []byte{0x31, 0xd5}
	requestService33_d5 = []byte{0x33, 0xd5}
	requestService33_c0 = []byte{0x33, 0xc0}
	requestService33_c8 = []byte{0x33, 0xc8}
	requestService33_d2 = []byte{0x33, 0xd2}
	requestService33_d4 = []byte{0x33, 0xd4}
	requestService33_da = []byte{0x33, 0xda}
	requestService33_c1 = []byte{0x33, 0xc1}
	requestService33_d7 = []byte{0x33, 0xd7}

	requestData00        = []byte{0x21, 0x00}
	requestData01        = []byte{0x21, 0x01}
	requestData02        = []byte{0x21, 0x02}
	requestData03        = []byte{0x21, 0x03}
	requestData05        = []byte{0x21, 0x05}
	requestData06        = []byte{0x21, 0x06}
	requestData07        = []byte{0x21, 0x07}
	requestData08        = []byte{0x21, 0x08}
	requestData09        = []byte{0x21, 0x09}
	requestData0A        = []byte{0x21, 0x0A}
	requestData0B        = []byte{0x21, 0x0B}
	requestData0C        = []byte{0x21, 0x0C}
	requestData0D        = []byte{0x21, 0x0D}
	requestData0F        = []byte{0x21, 0x0F}
	requestData10        = []byte{0x21, 0x10}
	requestData11        = []byte{0x21, 0x11}
	requestData12        = []byte{0x21, 0x12}
	requestData13        = []byte{0x21, 0x13}
	requestFaultsCommand = []byte{0x21, 0x19}
	requestData21        = []byte{0x21, 0x21}
	requestData25        = []byte{0x21, 0x25}
	requestData3A        = []byte{0x21, 0x3A}

	wokeResponse      = []byte{0xc1, 0xd5, 0x8f}
	startDiagResponse = []byte{0x50}
	seedResponse      = []byte{0x67, 0x01}
	keyAcceptResponse = []byte{0x67, 0x02}
	pongResponse      = []byte{0x7E}
	faultsResponse    = []byte{0x61, 0x19}

	responseData00 = []byte{0x61, 0x00}
	responseData01 = []byte{0x61, 0x01}
	responseData02 = []byte{0x61, 0x02}
	responseData03 = []byte{0x61, 0x03}
	responseData05 = []byte{0x61, 0x05}
	responseData06 = []byte{0x61, 0x06}
	responseData07 = []byte{0x61, 0x07}
	responseData08 = []byte{0x61, 0x08}
	responseData09 = []byte{0x61, 0x09}
	responseData0A = []byte{0x61, 0x0A}
	responseData0B = []byte{0x61, 0x0B}
	responseData0C = []byte{0x61, 0x0C}
	responseData0D = []byte{0x61, 0x0D}
	responseData0F = []byte{0x61, 0x0F}
	responseData10 = []byte{0x61, 0x10}
	responseData11 = []byte{0x61, 0x11}
	responseData12 = []byte{0x61, 0x12}
	responseData13 = []byte{0x61, 0x13}
	responseData21 = []byte{0x61, 0x21}
	responseData25 = []byte{0x61, 0x25}
	responseData3A = []byte{0x61, 0x3A}

	refusePing = []byte{0x7F, 0x3e, 0x10}

	userCommands = map[string][]byte{
		"clearfaults":  clearFaultsCommand,
		"learnimmo":    learnImmoCommand,
		"read722":      read722Command,
		"service13":    requestService13,
		"service31_d5": requestService31_d5,
		"service33_d5": requestService33_d5,
		"service33_c0": requestService33_c0,
		"service33_c8": requestService33_c8,
		"service33_d2": requestService33_d2,
		"service33_d4": requestService33_d4,
		"service33_da": requestService33_da,
		"service33_c1": requestService33_c1,
		"service33_d7": requestService33_d7,
	}
)

func (m *MEMS2J) sendCommand(command []byte) {
	finalCommand := []byte{byte(len(command))}
	finalCommand = append(finalCommand, command...)

	checksum := 0
	for i := 0; i < len(finalCommand); i++ {
		checksum += int(finalCommand[i])
	}
	checksum = checksum & 0xFF
	finalCommand = append(finalCommand, byte(checksum))

	m.lastSentCommand = finalCommand
	m.sp.Write(finalCommand)
}

func (m *MEMS2J) sendNextCommand(previousResponse []byte) {
	m.mu.Lock()
	cmd := m.userCommand
	m.mu.Unlock()

	if cmd != "" {
		command, ok := userCommands[cmd]
		if ok {
			m.logDebug("Running 2J user command: " + cmd)
			m.mu.Lock()
			m.userCommand = ""
			m.mu.Unlock()
			m.sendCommand(command)
			return
		} else {
			m.logDebug("Unknown user command: " + cmd)
			m.mu.Lock()
			m.userCommand = ""
			m.mu.Unlock()
		}
	}

	if utils.SlicesEqual(previousResponse, wokeResponse) {
		m.sendCommand(startDiagnostic)
	} else if utils.SlicesEqual(previousResponse, startDiagResponse) {
		m.sendCommand(requestSeed)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], seedResponse) {
		command := append(sendKey, byte(m.key>>8))
		command = append(command, byte(m.key&0xFF))
		m.sendCommand(command)
	} else if utils.SlicesEqual(previousResponse, keyAcceptResponse) {
		m.sendCommand(pingCommand)
	} else if utils.SlicesEqual(previousResponse, pongResponse) {
		m.sendCommand(requestFaultsCommand)
	} else if utils.SlicesEqual(previousResponse, faultsClearedResponse) {
		m.sendCommand(requestFaultsCommand)
	} else if utils.SlicesEqual(previousResponse, responseLearnImmoCommand) {
		m.sendCommand(requestData00)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], faultsResponse[0:2]) {
		m.sendCommand(requestData00)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData00) {
		m.sendCommand(requestData01)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData01) {
		m.sendCommand(requestData02)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData02) {
		m.sendCommand(requestData03)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData03) {
		m.sendCommand(requestData05)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData05) {
		m.sendCommand(requestData06)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData06) {
		m.sendCommand(requestData07)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData07) {
		m.sendCommand(requestData08)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData08) {
		m.sendCommand(requestData09)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData09) {
		m.sendCommand(requestData0A)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData0A) {
		m.sendCommand(requestData0B)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData0B) {
		m.sendCommand(requestData0C)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData0C) {
		m.sendCommand(requestData0D)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData0D) {
		m.sendCommand(requestData0F)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData0F) {
		m.sendCommand(requestData10)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData10) {
		m.sendCommand(requestData11)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData11) {
		m.sendCommand(requestData12)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData12) {
		m.sendCommand(requestData13)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData13) {
		m.sendCommand(requestData21)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData21) {
		m.sendCommand(requestData25)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData25) {
		m.sendCommand(requestData3A)
	} else if len(previousResponse) >= 2 && utils.SlicesEqual(previousResponse[0:2], responseData3A) {
		m.sendCommand(pingCommand)
	} else if utils.SlicesEqual(previousResponse, refusePing) {
		m.sendCommand(requestSeed)
	} else {
		m.logDebug("Falling back to ping command")
		m.sendCommand(pingCommand)
	}
}

func (m *MEMS2J) wakeUp() error {
	m.sp.SetBreak(false)
	time.Sleep(200 * time.Millisecond)

	m.sp.SetBreak(true)
	time.Sleep(25 * time.Millisecond)
	m.sp.SetBreak(false)
	time.Sleep(25 * time.Millisecond)

	time.Sleep(50 * time.Millisecond)

	m.sp.Write(initCommand)
	m.logDebug("Done sending init command")

	return nil
}

func (m *MEMS2J) loop() error {
	buffer := make([]byte, 0)
	lastReceivedData := utils.TimestampMs()
	timeoutMs := int64(1000)

	for utils.TimestampMs() < lastReceivedData+timeoutMs {
		newData := m.reader.Read()
		if len(newData) > 0 {
			lastReceivedData = utils.TimestampMs()
		}
		buffer = append(buffer, newData...)

		for len(buffer) > 0 && buffer[0] == 0x00 {
			m.logDebug("Cleared leading zeros")
			buffer = buffer[1:]
		}

		if len(buffer) == 0 {
			time.Sleep(1 * time.Millisecond)
			continue
		}

		if len(buffer) >= 5 && utils.SlicesEqual(buffer[0:len(initCommand)], initCommand) {
			m.logDebug("Got our init echo")
			buffer = buffer[len(initCommand):]
			continue
		}

		packetSize := int(buffer[0])
		if len(buffer) < packetSize+2 {
			time.Sleep(1 * time.Millisecond)
			continue
		}

		actualData := buffer[1 : packetSize+1]
		fullPacket := buffer[0 : packetSize+2]

		if len(m.lastSentCommand) > 0 && len(fullPacket) >= len(m.lastSentCommand) && utils.SlicesEqual(fullPacket[0:len(m.lastSentCommand)], m.lastSentCommand) {
			buffer = buffer[len(m.lastSentCommand):]
			continue
		}

		m.parseResponse(actualData)
		buffer = nil
		time.Sleep(25 * time.Millisecond)
		m.sendNextCommand(actualData)
	}

	if utils.TimestampMs() >= lastReceivedData+timeoutMs {
		return errors.New("MEMS 2J timed out")
	}
	m.logDebug("Read loop exited normally")

	return nil
}
