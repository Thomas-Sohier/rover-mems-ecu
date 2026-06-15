package navigation

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Navigation is the decoded form of a navigation characteristic write. When
// Active is false the head unit should clear its navigation display.
type Navigation struct {
	Active      bool   `json:"active"`
	Instruction string `json:"instruction"`
	Distance    string `json:"distance"`
	Eta         string `json:"eta"`
	IconID      string `json:"maneuver_icon_id"`
}

// wireNavigation is used purely for JSON decoding so we can treat null
// instruction/distance/eta/maneuver_icon_id as empty strings.
type wireNavigation struct {
	Active      bool    `json:"active"`
	Instruction *string `json:"instruction"`
	Distance    *string `json:"distance"`
	Eta         *string `json:"eta"`
	IconID      *string `json:"maneuver_icon_id"`
}

// ParseNavigation decodes a navigation characteristic write payload.
func ParseNavigation(data []byte) (Navigation, error) {
	var w wireNavigation
	if err := json.Unmarshal(data, &w); err != nil {
		return Navigation{}, fmt.Errorf("navigation: parse navigation: %w", err)
	}
	n := Navigation{Active: w.Active}
	if w.Instruction != nil {
		n.Instruction = *w.Instruction
	}
	if w.Distance != nil {
		n.Distance = *w.Distance
	}
	if w.Eta != nil {
		n.Eta = *w.Eta
	}
	if w.IconID != nil {
		n.IconID = *w.IconID
	}
	return n, nil
}

// ParseIconControl decodes a maneuver-icon-control characteristic write
// payload. Returns iconID, totalBytes, chunkCount, and any parse error.
func ParseIconControl(data []byte) (iconID string, totalBytes, chunkCount int, err error) {
	var v struct {
		IconID     string `json:"maneuver_icon_id"`
		TotalBytes int    `json:"total_bytes"`
		ChunkCount int    `json:"chunk_count"`
	}
	if err = json.Unmarshal(data, &v); err != nil {
		return "", 0, 0, fmt.Errorf("navigation: parse icon control: %w", err)
	}
	return v.IconID, v.TotalBytes, v.ChunkCount, nil
}

// iconTransfer tracks an in-progress chunked maneuver-icon upload.
type iconTransfer struct {
	iconID     string
	totalBytes int
	chunkCount int
	chunks     map[int][]byte
	received   int
}

// Snapshot is a point-in-time view of the store, safe to serialise. The PNG
// icon bytes themselves are fetched separately (see Icon); HasIcon reports
// whether the icon referenced by the current navigation has been received.
type Snapshot struct {
	Navigation Navigation `json:"navigation"`
	IconID     string     `json:"icon_id"`
	HasIcon    bool       `json:"has_icon"`
}

type subscriber struct {
	ch chan Snapshot
}

// Store is a mutex-protected store for the current navigation state and
// maneuver icon. Create with NewStore; do not copy after first use.
type Store struct {
	mu       sync.Mutex
	nav      Navigation
	iconID   string
	icon     []byte
	transfer *iconTransfer
	subs     []*subscriber
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return &Store{}
}

// HandleNavigation parses and stores a navigation write. Notifies subscribers.
func (s *Store) HandleNavigation(data []byte) error {
	n, err := ParseNavigation(data)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.nav = n
	snap := s.snapshotLocked()
	s.mu.Unlock()
	s.notify(snap)
	return nil
}

// HandleIconControl starts a new icon transfer, discarding any previous partial
// one.
func (s *Store) HandleIconControl(data []byte) error {
	iconID, totalBytes, chunkCount, err := ParseIconControl(data)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.transfer = &iconTransfer{
		iconID:     iconID,
		totalBytes: totalBytes,
		chunkCount: chunkCount,
		chunks:     make(map[int][]byte),
	}
	s.mu.Unlock()
	return nil
}

// HandleIconChunk processes a single icon-data write. The payload must be at
// least 2 bytes: the first two are the big-endian chunk index, the rest are
// PNG payload. Duplicate chunk indices overwrite without error. When the
// accumulated received bytes equal totalBytes the icon is assembled and
// subscribers are notified.
func (s *Store) HandleIconChunk(data []byte) error {
	if len(data) < 2 {
		return errors.New("navigation: icon chunk too short")
	}
	idx := int(binary.BigEndian.Uint16(data[:2]))
	// Copy: the BLE stack may reuse the write buffer after the handler returns.
	payload := make([]byte, len(data)-2)
	copy(payload, data[2:])

	s.mu.Lock()

	if s.transfer == nil {
		s.mu.Unlock()
		return errors.New("navigation: no icon transfer in progress")
	}
	t := s.transfer

	// Duplicate: subtract old length before overwriting.
	if prev, dup := t.chunks[idx]; dup {
		t.received -= len(prev)
	}
	t.chunks[idx] = payload
	t.received += len(payload)

	if t.received > t.totalBytes {
		s.transfer = nil
		s.mu.Unlock()
		return fmt.Errorf("navigation: icon chunk overflow: received %d > totalBytes %d", t.received, t.totalBytes)
	}

	if t.received < t.totalBytes {
		s.mu.Unlock()
		return nil
	}

	// Assembly complete.
	assembled := make([]byte, 0, t.totalBytes)
	for i := 0; i < t.chunkCount; i++ {
		assembled = append(assembled, t.chunks[i]...)
	}
	s.icon = assembled
	s.iconID = t.iconID
	s.transfer = nil
	snap := s.snapshotLocked()
	s.mu.Unlock()
	s.notify(snap)
	return nil
}

// snapshotLocked builds a Snapshot. Caller must hold s.mu. HasIcon is true only
// when the stored icon matches the icon referenced by the current navigation.
func (s *Store) snapshotLocked() Snapshot {
	hasIcon := len(s.icon) > 0 && s.iconID != "" && s.iconID == s.nav.IconID
	return Snapshot{
		Navigation: s.nav,
		IconID:     s.iconID,
		HasIcon:    hasIcon,
	}
}

// Snapshot returns a point-in-time copy of the store state.
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// Icon returns the current maneuver-icon PNG bytes and icon ID. ok is false
// when no icon has been received.
func (s *Store) Icon() (iconID string, png []byte, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.icon) == 0 {
		return "", nil, false
	}
	return s.iconID, s.icon, true
}

// Subscribe returns a channel that receives a Snapshot whenever navigation or
// the maneuver icon changes, and an unsubscribe function. The channel is
// buffered (size 8); sends are non-blocking (dropped if full). Call unsubscribe
// when done.
func (s *Store) Subscribe() (ch <-chan Snapshot, unsubscribe func()) {
	sub := &subscriber{ch: make(chan Snapshot, 8)}
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

func (s *Store) notify(snap Snapshot) {
	s.mu.Lock()
	subs := make([]*subscriber, len(s.subs))
	copy(subs, s.subs)
	s.mu.Unlock()
	for _, sub := range subs {
		select {
		case sub.ch <- snap:
		default:
		}
	}
}
