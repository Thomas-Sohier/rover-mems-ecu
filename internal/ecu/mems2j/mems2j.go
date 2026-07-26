package mems2j

import (
	"context"
	"errors"
	"fmt"
	"time"

	"rover-mems-agent/internal/ecu"
	"rover-mems-agent/internal/serial"
	"rover-mems-agent/pkg/utils"
)

func init() {
	ecu.Register("2J", NewMEMS2J)
}

// MEMS2J handles MEMS 2J ECUs (KV6, K-series).
type MEMS2J struct {
	state *ecu.State

	sp              serial.Port
	reader          *serial.Reader
	lastSentCommand []byte
	seed            int
	key             int
}

// NewMEMS2J creates a new MEMS 2J ECU handler.
func NewMEMS2J(state *ecu.State, cfg ecu.Config) (ecu.ECU, error) {
	state.DebugMode = cfg.DebugMode
	return &MEMS2J{
		state:  state,
		reader: serial.NewReader(),
	}, nil
}

func (m *MEMS2J) Connect(_ context.Context, portName string) error {
	m.logDebug("Connecting to MEMS 2J ECU")
	m.state.Lock()
	m.state.Connected = false
	m.state.Unlock()

	sp, err := serial.Open(portName, 10400, serial.NoParity)
	if err != nil {
		return fmt.Errorf("open serial port %s: %w", portName, err)
	}
	m.sp = sp

	// Small blocking timeout instead of 0 (non-blocking) so the reader
	// goroutine sleeps between frames rather than busy-looping a core.
	if err = sp.SetReadTimeout(50 * time.Millisecond); err != nil {
		sp.Close()
		return err
	}

	m.logDebug("Serial cable set to 10400 8N1")

	m.reader.Start(sp)

	if err := m.wakeUp(); err != nil {
		sp.Close()
		return err
	}

	return nil
}

func (m *MEMS2J) ReadData(ctx context.Context) error {
	return m.loop(ctx)
}

func (m *MEMS2J) Close() error {
	m.state.Lock()
	m.state.Connected = false
	m.state.Unlock()
	m.reader.Stop()
	if m.sp != nil {
		return m.sp.Close()
	}
	return nil
}

func (m *MEMS2J) Type() string {
	return "2J"
}

func (m *MEMS2J) logDebug(msg string) { m.state.LogDebug(msg) }

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

	// pollOrder is the fixed cycle of requests issued once the session is
	// unlocked: the fault list first, then every data PID, then back to ping.
	// The ECU answers request 21 xx with response 61 xx, which is what lets
	// nextAfterPoll key itself off the request bytes.
	pollOrder = [][]byte{
		requestFaultsCommand,
		requestData00, requestData01, requestData02, requestData03,
		requestData05, requestData06, requestData07, requestData08,
		requestData09, requestData0A, requestData0B, requestData0C,
		requestData0D, requestData0F, requestData10, requestData11,
		requestData12, requestData13, requestData21, requestData25,
		requestData3A,
	}

	// nextAfterPoll maps a two-byte response header to the request that follows
	// it. Derived from pollOrder so the polling sequence is stated exactly once.
	nextAfterPoll = buildNextAfterPoll()

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

// buildNextAfterPoll turns pollOrder into a response-header -> next-request
// lookup. The last entry wraps back to ping, restarting the cycle.
func buildNextAfterPoll() map[[2]byte][]byte {
	m := make(map[[2]byte][]byte, len(pollOrder))
	for i, req := range pollOrder {
		next := pingCommand
		if i+1 < len(pollOrder) {
			next = pollOrder[i+1]
		}
		m[[2]byte{0x61, req[1]}] = next
	}
	return m
}

// takeUserCommand consumes any pending user command, returning the bytes to
// send. ok is false when nothing is pending or the name is unknown; either way
// the pending command is cleared so an unrecognised name cannot wedge the loop.
func (m *MEMS2J) takeUserCommand() (command []byte, ok bool) {
	m.state.Lock()
	name := m.state.UserCommand
	m.state.UserCommand = ""
	m.state.Unlock()

	if name == "" {
		return nil, false
	}
	command, ok = userCommands[name]
	if !ok {
		m.logDebug("Unknown user command: " + name)
		return nil, false
	}
	m.logDebug("Running 2J user command: " + name)
	return command, true
}

// sendCommand frames and writes a 2J command.
//
// Unlike the bare single bytes of MEMS 1.x, the 2J (KWP-style) protocol uses
// length-prefixed packets with a trailing checksum: [len][payload...][checksum],
// where len counts only the payload and checksum is the low byte of the sum of
// the length byte plus every payload byte. We keep the framed bytes in
// lastSentCommand so the read loop can recognise and discard the K-line echo of
// what we just sent.
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
	if _, err := m.sp.Write(finalCommand); err != nil {
		m.state.LogDebugf("serial write failed: %v", err)
	}
}

// sendNextCommand advances the 2J state machine based on the previous reply.
//
// After the wake-up the 2J requires a sequence before it streams data:
// startDiagnostic (10 A0) -> requestSeed (27 01) -> sendKey (27 02 + key) ->
// ping (3E). The seed/key step is the security-access challenge: the ECU returns
// a 16-bit seed and we must answer with the matching key (see ecu.GenerateKey);
// a seed of 0 means we are already unlocked. Once unlocked we poll the fault
// list (21 19) then cycle through the data PIDs (21 00 .. 21 3A) and loop back to
// ping. A pending user command pre-empts the sequence, and a refused ping
// (7F 3E 10) means the session dropped, so we restart from requestSeed.
func (m *MEMS2J) sendNextCommand(previousResponse []byte) {
	if command, ok := m.takeUserCommand(); ok {
		m.sendCommand(command)
		return
	}

	// Handshake replies are fixed whole frames.
	switch {
	case utils.SlicesEqual(previousResponse, wokeResponse):
		// Settle time before the ECU will accept the diagnostic session request.
		// Kept here rather than in parseResponse, which runs with the State write
		// lock held and so must not block.
		time.Sleep(50 * time.Millisecond)
		m.sendCommand(startDiagnostic)
		return
	case utils.SlicesEqual(previousResponse, startDiagResponse):
		m.sendCommand(requestSeed)
		return
	case utils.SlicesEqual(previousResponse, keyAcceptResponse):
		m.sendCommand(pingCommand)
		return
	case utils.SlicesEqual(previousResponse, pongResponse),
		utils.SlicesEqual(previousResponse, faultsClearedResponse):
		m.sendCommand(requestFaultsCommand)
		return
	case utils.SlicesEqual(previousResponse, responseLearnImmoCommand):
		m.sendCommand(requestData00)
		return
	case utils.SlicesEqual(previousResponse, refusePing):
		// Session dropped; re-run security access.
		m.sendCommand(requestSeed)
		return
	}

	// Everything else is recognised by its two-byte header.
	if len(previousResponse) >= 2 {
		if utils.SlicesEqual(previousResponse[0:2], seedResponse) {
			// Build on a fresh slice: appending to the package-level sendKey would
			// write through to it the moment that literal gained spare capacity.
			command := make([]byte, 0, len(sendKey)+2)
			command = append(command, sendKey...)
			command = append(command, byte(m.key>>8), byte(m.key&0xFF))
			m.sendCommand(command)
			return
		}
		if next, ok := nextAfterPoll[[2]byte{previousResponse[0], previousResponse[1]}]; ok {
			m.sendCommand(next)
			return
		}
	}

	m.logDebug("Falling back to ping command")
	m.sendCommand(pingCommand)
}

// wakeUp performs the 2J fast-init and sends the start-communication request.
//
// The 2J uses a KWP2000-style fast-init rather than the 1.9 slow-init: instead
// of bit-banging an address, we assert a single break "wake" pulse (25 ms low,
// 25 ms high) to get the ECU's attention, then immediately transmit the
// start-communication frame initCommand (81 13 F7 81 0C). The ECU answers with
// C1 D5 8F (wokeResponse), which the read loop recognises to mark us connected.
// The leading 200 ms of idle line ensures the pulse is seen cleanly.
func (m *MEMS2J) wakeUp() error {
	time.Sleep(200 * time.Millisecond) // idle line high

	if err := m.sp.Break(25 * time.Millisecond); err != nil { // wake pulse: line low
		return fmt.Errorf("2J wake pulse: %w", err)
	}
	time.Sleep(25 * time.Millisecond) // line high

	time.Sleep(50 * time.Millisecond)

	if _, err := m.sp.Write(initCommand); err != nil {
		return fmt.Errorf("2J init command: %w", err)
	}
	m.logDebug("Done sending init command")

	return nil
}

// loop reads framed 2J packets and drives the request/response cycle.
//
// Reads come through a background goroutine (serial.Reader) because Linux serial
// reads block even with a timeout, so we pull from its buffer instead of reading
// the port directly. Per packet we: strip leading 0x00 padding; drop our own
// init echo; wait until the full [len][payload][checksum] frame has arrived
// (len at buffer[0], so packetSize+2 bytes total); discard the echo of the
// command we just sent; then parse the payload and ask sendNextCommand for the
// next request. If no bytes arrive for timeoutMs the ECU is considered gone.
func (m *MEMS2J) loop(ctx context.Context) error {
	buffer := make([]byte, 0)
	lastReceivedData := utils.TimestampMs()
	timeoutMs := int64(1000)

	for utils.TimestampMs() < lastReceivedData+timeoutMs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

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
