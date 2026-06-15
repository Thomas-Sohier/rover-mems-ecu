package notification

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Alert is the decoded form of an alert characteristic write: a single
// fire-once notification forwarded from an allowlisted app.
type Alert struct {
	App      string `json:"app"`
	Title    string `json:"title"`
	Text     string `json:"text"`
	PostedAt int64  `json:"posted_at"`
}

// ParseAlert decodes an alert characteristic write payload.
func ParseAlert(data []byte) (Alert, error) {
	var a Alert
	if err := json.Unmarshal(data, &a); err != nil {
		return Alert{}, fmt.Errorf("notification: parse alert: %w", err)
	}
	return a, nil
}

type subscriber struct {
	ch chan Alert
}

// Store fans out one-shot alerts to subscribers and retains the most recent one
// for a point-in-time read. Create with NewStore; do not copy after first use.
//
// Alerts are events, not state: subscribers receive each alert as it arrives,
// and the store deliberately does not replay past alerts to late joiners.
type Store struct {
	mu      sync.Mutex
	last    Alert
	hasLast bool
	subs    []*subscriber
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return &Store{}
}

// HandleAlert parses an alert write, records it as the most recent alert, and
// fans it out to subscribers.
func (s *Store) HandleAlert(data []byte) error {
	a, err := ParseAlert(data)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.last = a
	s.hasLast = true
	subs := make([]*subscriber, len(s.subs))
	copy(subs, s.subs)
	s.mu.Unlock()
	for _, sub := range subs {
		select {
		case sub.ch <- a:
		default:
		}
	}
	return nil
}

// Last returns the most recent alert. ok is false when none has been received.
func (s *Store) Last() (alert Alert, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last, s.hasLast
}

// Subscribe returns a channel that receives every alert as it arrives, and an
// unsubscribe function. The channel is buffered (size 8); sends are
// non-blocking (dropped if full). Call unsubscribe when done.
func (s *Store) Subscribe() (ch <-chan Alert, unsubscribe func()) {
	sub := &subscriber{ch: make(chan Alert, 8)}
	s.mu.Lock()
	s.subs = append(s.subs, sub)
	s.mu.Unlock()
	return sub.ch, func() {
		s.mu.Lock()
		for i, v := range s.subs {
			if v == sub {
				s.subs = append(s.subs[:i], s.subs[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
	}
}
