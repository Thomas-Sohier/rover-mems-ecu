package serial

import (
	"errors"
	"sync"

	"github.com/distributed/sers"
)

// Reader provides non-blocking serial reads using a channel buffer.
type Reader struct {
	channel chan byte
	done    chan struct{}
	once    sync.Once
}

// NewReader creates a new serial reader.
func NewReader() *Reader {
	return &Reader{
		channel: make(chan byte, 1024),
		done:    make(chan struct{}),
	}
}

// Start begins the read routine for the given serial port.
// A new done channel is initialised so Start can be called again after Stop.
func (r *Reader) Start(sp sers.SerialPort) {
	r.done = make(chan struct{})
	r.once = sync.Once{}
	done := r.done
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			rb := make([]byte, 256)
			n, err := sp.Read(rb[:])
			if err != nil {
				var te interface{ Timeout() bool }
				if !errors.As(err, &te) || !te.Timeout() {
					return
				}
			}
			rb = rb[0:n]
			for i := 0; i < n; i++ {
				select {
				case r.channel <- rb[i]:
				case <-done:
					return
				}
			}
		}
	}()
}

// Stop signals the read goroutine to exit. Safe to call multiple times.
func (r *Reader) Stop() {
	r.once.Do(func() { close(r.done) })
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
