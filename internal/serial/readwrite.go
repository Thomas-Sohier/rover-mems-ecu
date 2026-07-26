package serial

import (
	"errors"
	"sync"
)

// Reader provides non-blocking serial reads using a channel buffer.
//
// A Reader drives one Start/Stop cycle at a time. Start may be called again
// after Stop, which re-arms the stop signal and drains anything the previous
// port left buffered so a new session cannot read the old one's bytes.
type Reader struct {
	channel chan byte

	// mu guards done and once, which Start replaces on each cycle.
	mu   sync.Mutex
	done chan struct{}
	once sync.Once
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
func (r *Reader) Start(sp Port) {
	// Discard anything the previous cycle left buffered: those bytes belong to a
	// port that is no longer open, and delivering them would corrupt the framing
	// of the new session.
	r.drain()

	r.mu.Lock()
	r.done = make(chan struct{})
	r.once = sync.Once{}
	done := r.done
	r.mu.Unlock()

	go func() {
		// Reused across iterations: bytes are pushed onto the channel before the
		// next Read, so the buffer is never aliased.
		rb := make([]byte, 256)
		for {
			select {
			case <-done:
				return
			default:
			}
			n, err := sp.Read(rb)
			if err != nil {
				var te interface{ Timeout() bool }
				if !errors.As(err, &te) || !te.Timeout() {
					return
				}
			}
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

// Stop signals the read goroutine to exit. Safe to call multiple times, and
// safe to call concurrently with Start.
func (r *Reader) Stop() {
	r.mu.Lock()
	once, done := &r.once, r.done
	r.mu.Unlock()

	if done == nil {
		return
	}
	once.Do(func() { close(done) })
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

// drain discards any buffered bytes without returning them.
func (r *Reader) drain() {
	for {
		select {
		case <-r.channel:
		default:
			return
		}
	}
}
