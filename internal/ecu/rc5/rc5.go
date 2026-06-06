package rc5

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"rover-mems-agent/internal/ecu"

	"github.com/distributed/sers"
)

func init() {
	ecu.Register("rc5", NewRC5)
}

var (
	pingCommand          = []byte{0x82, 0x00, 0x7D}
	requestFaultsCommand = []byte{0x82, 0x33, 0x4A}
	clearFaultsCommand   = []byte{0x82, 0xC3, 0xBA}

	wokeResponse          = []byte{0x55, 0x06, 0x3B}
	pongResponse          = []byte{0xC2, 0x00, 0x3D}
	faultsResponse        = []byte{0x33}
	faultsClearedResponse = []byte{0xC2, 0xC3, 0x7A}

	userCommands = map[string][]byte{
		"clearfaults": clearFaultsCommand,
	}

	faultCodes = map[int]string{
		0x150A: "Driver airbag shorted to battery positive",
		0x150B: "Driver airbag shorted to battery negative",
		0x150C: "Driver airbag high resistance",
		0x150D: "Driver airbag low resistance",
		0x150E: "Driver airbag squib circuit",
		0x1512: "Passenger airbag squib short to battery positive",
		0x1513: "Passenger airbag 1 short to battery negative",
		0x1514: "Passenger airbag 1 high resistance",
		0x1515: "Passenger airbag 1 low resistance",
		0x1516: "Passenger airbag 1 squib circuit",
		0x151A: "Pretensioner short to battery positive",
		0x151B: "Pretensioner short to battery negative",
		0x151C: "Passenger airbag 2 high resistance",
		0x151D: "Passenger airbag 2 low resistance",
		0x151E: "Passenger airbag 2 squib circuit",
		0x1524: "Right pretensioner high resistance",
		0x1525: "Right pretensioner low resistance",
		0x1526: "Right pretensioner squib circuit",
		0x152C: "Left pretensioner high resistance",
		0x152D: "Left pretensioner low resistance",
		0x152E: "Left pretensioner squib circuit",
		0x160C: "SRS warning lamp short circuit",
		0x160D: "SRS warning lamp open circuit",
		0x160E: "SRS warning lamp driver",
		0x250A: "(Historic) Driver airbag shorted to battery positive",
		0x250B: "(Historic) Driver airbag shorted to battery negative",
		0x250C: "(Historic) Driver airbag high resistance",
		0x250D: "(Historic) Driver airbag low resistance",
		0x250E: "(Historic) Driver airbag squib circuit",
		0x2512: "(Historic) Passenger airbag squib short to battery positive",
		0x2513: "(Historic) Passenger airbag 1 short to battery negative",
		0x2514: "(Historic) Passenger airbag 1 high resistance",
		0x2515: "(Historic) Passenger airbag 1 low resistance",
		0x2516: "(Historic) Passenger airbag 1 squib circuit",
		0x251A: "(Historic) Pretensioner short to battery positive",
		0x251B: "(Historic) Pretensioner short to battery negative",
		0x251C: "(Historic) Passenger airbag 2 high resistance",
		0x251D: "(Historic) Passenger airbag 2 low resistance",
		0x251E: "(Historic) Passenger airbag 2 squib circuit",
		0x2524: "(Historic) Right pretensioner high resistance",
		0x2525: "(Historic) Right pretensioner low resistance",
		0x2526: "(Historic) Right pretensioner squib circuit",
		0x252C: "(Historic) Left pretensioner high resistance",
		0x252D: "(Historic) Left pretensioner low resistance",
		0x252E: "(Historic) Left pretensioner squib circuit",
		0x260C: "(Historic) SRS warning lamp short circuit",
		0x260D: "(Historic) SRS warning lamp open circuit",
		0x260E: "(Historic) SRS warning lamp driver",
		0x0000: "0x0000 Unknown fault, power cycle and try again",
	}
)

// RC5 handles RC5 airbag ECUs.
type RC5 struct {
	state *ecu.State
	sp    sers.SerialPort
}

// NewRC5 creates a new RC5 ECU handler.
func NewRC5(state *ecu.State, cfg ecu.Config) (ecu.ECU, error) {
	state.DebugMode = cfg.DebugMode
	return &RC5{state: state}, nil
}

// Connect opens the port at 2400 8N1 and wakes the RC5 airbag ECU.
//
// RC5 is the slowest of the supported buses (2400 baud) and needs a fixed
// break/mark pattern rather than an addressed init: a long idle, then a
// hand-timed sequence of break-on / break-off pulses (see the SetBreak calls)
// that the ECU recognises as the wake signal. A successful wake is the ECU
// sending wokeResponse (55 06 3B); anything else means we are talking to the
// wrong ECU/baud and we abort rather than guess.
func (r *RC5) Connect(_ context.Context, portName string) error {
	r.state.LogDebug("Connecting to RC5 ECU")
	r.state.Lock()
	r.state.Connected = false
	r.state.Unlock()

	sp, err := sers.Open(portName)
	if err != nil {
		return fmt.Errorf("open serial port %s: %w", portName, err)
	}
	r.sp = sp

	err = sp.SetMode(2400, 8, sers.N, 1, sers.NO_HANDSHAKE)
	if err != nil {
		sp.Close()
		return fmt.Errorf("set serial mode: %w", err)
	}

	err = sp.SetReadParams(0, 0.001)
	if err != nil {
		sp.Close()
		return err
	}

	mode, _ := sp.GetMode()
	r.state.LogDebug("Serial cable set to:")
	r.state.LogDebug(mode)

	sp.SetBreak(false)
	time.Sleep(2000 * time.Millisecond)
	sp.SetBreak(true)
	time.Sleep(200 * time.Millisecond)
	sp.SetBreak(false)
	time.Sleep(400 * time.Millisecond)
	sp.SetBreak(true)
	time.Sleep(400 * time.Millisecond)
	sp.SetBreak(false)
	time.Sleep(400 * time.Millisecond)
	sp.SetBreak(true)
	time.Sleep(400 * time.Millisecond)
	sp.SetBreak(false)
	time.Sleep(200 * time.Millisecond)

	initBuffer := make([]byte, 0)
	initLoops := 0
	initLoopsLimit := 100

	for initLoops < initLoopsLimit {
		initLoops++
		if initLoops > 1 {
			time.Sleep(10 * time.Millisecond)
		}

		rb := make([]byte, 128)
		n, err := sp.Read(rb[:])
		if err != nil {
			continue
		}
		if n == 0 {
			continue
		}

		rb = rb[0:n]
		initBuffer = append(initBuffer, rb...)

		for len(initBuffer) > 0 && initBuffer[0] == 0x00 {
			initBuffer = initBuffer[1:]
		}

		if len(initBuffer) < 3 {
			continue
		}

		if slicesEqual(initBuffer[0:3], wokeResponse) {
			r.state.LogDebug("RC5 woke up")
			r.state.Lock()
			r.state.Connected = true
			r.state.Unlock()
			return nil
		} else {
			sp.Close()
			return errors.New("Unsure what RC5 sent back, aborting")
		}
	}

	sp.Close()
	return errors.New("Timed out waiting for RC5 to wake up")
}

// ReadData runs the RC5 request/response loop.
//
// RC5 commands are fixed 3-byte frames [82][service][checksum] (e.g. ping
// 82 00 7D, request faults 82 33 4A, clear faults 82 C3 BA); replies are also
// recognised by their first 3 bytes. As a half-duplex bus our own sent frames
// are echoed back, so any buffer starting with a known command frame is
// discarded. A fault reply is variable length: its first byte encodes the count
// (length = byte[0] - 0xC0 + 1), so we wait for that many bytes before parsing.
// The poll alternates ping -> request faults.
func (r *RC5) ReadData(ctx context.Context) error {
	time.Sleep(500 * time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	r.sendNextCommand(wokeResponse)

	buffer := make([]byte, 0)
	readLoops := 0

	for readLoops < 100 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		readLoops++
		if readLoops > 1 {
			time.Sleep(10 * time.Millisecond)
		}

		rb := make([]byte, 128)
		n, _ := r.sp.Read(rb[:])
		rb = rb[0:n]
		buffer = append(buffer, rb...)
		if n > 0 {
			readLoops = 0
		}

		if len(buffer) == 0 {
			continue
		}

		if len(buffer) >= 3 {
			if slicesEqual(buffer[0:3], pingCommand) ||
				slicesEqual(buffer[0:3], requestFaultsCommand) ||
				slicesEqual(buffer[0:3], clearFaultsCommand) {
				buffer = buffer[3:]
				continue
			}

			if slicesEqual(buffer[0:3], pongResponse) {
				r.state.LogDebug("< PONG from ECU")
				buffer = buffer[3:]
				time.Sleep(200 * time.Millisecond)
				r.sendNextCommand(pongResponse)
				continue
			}

			if slicesEqual(buffer[0:3], faultsClearedResponse) {
				r.state.LogDebug("< FAULT CODES CLEARED")
				r.state.Lock()
				r.state.Alert = "ECU reports faults cleared"
				r.state.Unlock()
				buffer = buffer[3:]
				time.Sleep(200 * time.Millisecond)
				r.sendNextCommand(faultsClearedResponse)
				continue
			}

			if len(buffer) > 2 && buffer[1] == faultsResponse[0] {
				expectedLength := buffer[0]
				expectedLength = expectedLength - 0xC0 + 1
				if len(buffer) < int(expectedLength) {
					continue
				}
				r.state.LogDebug("< FAULTS Got fault codes!")
				r.parseFaults(buffer)
				buffer = buffer[expectedLength:]
				time.Sleep(200 * time.Millisecond)
				r.sendNextCommand(faultsResponse)
				continue
			}
		}
	}

	if readLoops == 100 {
		return errors.New("readloop timed out")
	}
	r.state.LogDebug("fell out of readloop")
	return nil
}

// sendNextCommand chooses the next RC5 frame: a pong leads to a fault request, a
// wake or fault reply leads back to ping, and a faults-cleared reply re-reads
// faults. A pending user command (e.g. clear faults) pre-empts the cycle.
func (r *RC5) sendNextCommand(previousResponse []byte) {
	r.state.Lock()
	cmd := r.state.UserCommand
	r.state.Unlock()

	if cmd != "" {
		command, ok := userCommands[cmd]
		if ok {
			r.state.Lock()
			r.state.UserCommand = ""
			r.state.Unlock()
			if _, err := r.sp.Write(command); err != nil {
				r.state.LogDebugf("serial write failed: %v", err)
			}
			return
		} else {
			r.state.LogDebug("Asked to perform a user command but don't understand it")
		}
	}

	r.state.Lock()
	r.state.UserCommand = ""
	r.state.Unlock()

	if slicesEqual(previousResponse, pongResponse) {
		if _, err := r.sp.Write(requestFaultsCommand); err != nil {
			r.state.LogDebugf("serial write failed: %v", err)
		}
	} else if slicesEqual(previousResponse, wokeResponse) || slicesEqual(previousResponse, faultsResponse) {
		if _, err := r.sp.Write(pingCommand); err != nil {
			r.state.LogDebugf("serial write failed: %v", err)
		}
	} else if slicesEqual(previousResponse, faultsClearedResponse) {
		if _, err := r.sp.Write(requestFaultsCommand); err != nil {
			r.state.LogDebugf("serial write failed: %v", err)
		}
	} else {
		if _, err := r.sp.Write(pingCommand); err != nil {
			r.state.LogDebugf("serial write failed: %v", err)
		}
	}
}

// parseFaults decodes the RC5 fault reply. After dropping the 2-byte header the
// remainder is a list of 16-bit big-endian fault codes; each is looked up in
// faultCodes for an airbag-specific description (driver/passenger squibs,
// pretensioners, SRS lamp, with 0x25xx/0x26xx being the historic variants).
func (r *RC5) parseFaults(buffer []byte) {
	buffer = buffer[2:]
	numFaults := len(buffer) / 2
	r.state.LogDebugf("num faults: %d", numFaults)

	faults := []string{}

	for i := 0; i < numFaults; i++ {
		fault := int(buffer[i*2])<<8 + int(buffer[(i*2)+1])
		faultText, ok := faultCodes[fault]
		if !ok {
			faultText = "unknown fault: " + strconv.Itoa(fault)
		}
		faults = append(faults, faultText)
	}

	r.state.Lock()
	r.state.Faults = faults
	r.state.Unlock()
}

func (r *RC5) Close() error {
	r.state.Lock()
	r.state.Connected = false
	r.state.Unlock()
	if r.sp != nil {
		return r.sp.Close()
	}
	return nil
}

func (r *RC5) Type() string {
	return "rc5"
}

func slicesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
