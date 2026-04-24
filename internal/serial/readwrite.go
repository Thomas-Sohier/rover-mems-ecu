package serial

import (
	"github.com/distributed/sers"
)

// Reader provides non-blocking serial reads using a channel buffer.
type Reader struct {
	channel chan byte
}

// NewReader creates a new serial reader.
func NewReader() *Reader {
	return &Reader{
		channel: make(chan byte, 1024),
	}
}

// Start begins the read routine for the given serial port.
func (r *Reader) Start(sp sers.SerialPort) {
	go func() {
		for {
			rb := make([]byte, 256)
			n, _ := sp.Read(rb[:])
			rb = rb[0:n]
			for i := 0; i < n; i++ {
				r.channel <- rb[i]
			}
		}
	}()
}

// Read returns all currently available data from the channel (non-blocking).
func (r *Reader) Read() []byte {
	buffer := make([]byte, 0)
outer:
	for {
		select {
		case msg := <-r.channel:
			buffer = append(buffer, msg)
		default:
			break outer
		}
	}
	return buffer
}
