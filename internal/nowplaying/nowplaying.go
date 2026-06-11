package nowplaying

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Metadata is the decoded form of the phone's metadata JSON write.
type Metadata struct {
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
	State      string `json:"state"`
	PositionMs int64  `json:"position_ms"`
	DurationMs int64  `json:"duration_ms"`
	// ArtID is empty when the wire value is null.
	ArtID string `json:"art_id"`
}

// wireMetadata is used purely for JSON decoding so we can detect a null art_id.
type wireMetadata struct {
	Title      string  `json:"title"`
	Artist     string  `json:"artist"`
	Album      string  `json:"album"`
	State      string  `json:"state"`
	PositionMs int64   `json:"position_ms"`
	DurationMs int64   `json:"duration_ms"`
	ArtID      *string `json:"art_id"`
}

// ParseMetadata decodes a metadata characteristic write payload.
func ParseMetadata(data []byte) (Metadata, error) {
	var w wireMetadata
	if err := json.Unmarshal(data, &w); err != nil {
		return Metadata{}, fmt.Errorf("nowplaying: parse metadata: %w", err)
	}
	m := Metadata{
		Title:      w.Title,
		Artist:     w.Artist,
		Album:      w.Album,
		State:      w.State,
		PositionMs: w.PositionMs,
		DurationMs: w.DurationMs,
	}
	if w.ArtID != nil {
		m.ArtID = *w.ArtID
	}
	return m, nil
}

// artTransfer tracks an in-progress chunked art upload.
type artTransfer struct {
	artID      string
	totalBytes int
	chunkCount int
	chunks     map[int][]byte
	received   int
}

// ParseArtControl decodes an art-control characteristic write payload.
// Returns artID, totalBytes, chunkCount, and any parse error.
func ParseArtControl(data []byte) (artID string, totalBytes, chunkCount int, err error) {
	var v struct {
		ArtID      string `json:"art_id"`
		TotalBytes int    `json:"total_bytes"`
		ChunkCount int    `json:"chunk_count"`
	}
	if err = json.Unmarshal(data, &v); err != nil {
		return "", 0, 0, fmt.Errorf("nowplaying: parse art control: %w", err)
	}
	return v.ArtID, v.TotalBytes, v.ChunkCount, nil
}

// Snapshot is a point-in-time view of the store, safe to serialise.
type Snapshot struct {
	Metadata Metadata `json:"metadata"`
	ArtID    string   `json:"art_id"`
	HasArt   bool     `json:"has_art"`
}

type subscriber struct {
	ch chan Snapshot
}

// Store is a mutex-protected store for current metadata and cover art. Create
// with NewStore; do not copy after first use.
type Store struct {
	mu       sync.Mutex
	metadata Metadata
	artID    string
	art      []byte
	transfer *artTransfer
	subs     []*subscriber
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return &Store{}
}

// HandleMetadata parses and stores a metadata write. Notifies subscribers.
func (s *Store) HandleMetadata(data []byte) error {
	m, err := ParseMetadata(data)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.metadata = m
	snap := s.snapshotLocked()
	s.mu.Unlock()
	s.notify(snap)
	return nil
}

// HandleArtControl starts a new art transfer, discarding any previous partial one.
func (s *Store) HandleArtControl(data []byte) error {
	artID, totalBytes, chunkCount, err := ParseArtControl(data)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.transfer = &artTransfer{
		artID:      artID,
		totalBytes: totalBytes,
		chunkCount: chunkCount,
		chunks:     make(map[int][]byte),
	}
	s.mu.Unlock()
	return nil
}

// HandleArtChunk processes a single art-data write. The payload must be at
// least 2 bytes: the first two are the big-endian chunk index, the rest are
// JPEG payload. Duplicate chunk indices overwrite without error. When the
// accumulated received bytes equal totalBytes the art is assembled and
// subscribers are notified.
func (s *Store) HandleArtChunk(data []byte) error {
	if len(data) < 2 {
		return errors.New("nowplaying: art chunk too short")
	}
	idx := int(binary.BigEndian.Uint16(data[:2]))
	// Copy: the BLE stack may reuse the write buffer after the handler returns.
	payload := make([]byte, len(data)-2)
	copy(payload, data[2:])

	s.mu.Lock()

	if s.transfer == nil {
		s.mu.Unlock()
		return errors.New("nowplaying: no art transfer in progress")
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
		return fmt.Errorf("nowplaying: art chunk overflow: received %d > totalBytes %d", t.received, t.totalBytes)
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
	s.art = assembled
	s.artID = t.artID
	s.transfer = nil
	snap := s.snapshotLocked()
	s.mu.Unlock()
	s.notify(snap)
	return nil
}

// snapshotLocked builds a Snapshot. Caller must hold s.mu.
func (s *Store) snapshotLocked() Snapshot {
	return Snapshot{
		Metadata: s.metadata,
		ArtID:    s.artID,
		HasArt:   len(s.art) > 0,
	}
}

// Snapshot returns a point-in-time copy of the store state.
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// Art returns the current cover art JPEG bytes and art ID. ok is false when
// no art has been received.
func (s *Store) Art() (artID string, jpeg []byte, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.art) == 0 {
		return "", nil, false
	}
	return s.artID, s.art, true
}

// Subscribe returns a channel that receives a Snapshot whenever metadata or
// art changes, and an unsubscribe function. The channel is buffered (size 8);
// sends are non-blocking (dropped if full). Call unsubscribe when done.
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
