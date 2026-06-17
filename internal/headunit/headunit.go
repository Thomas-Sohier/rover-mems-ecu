package headunit

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
)

// Store is a transparent relay between the phone (BLE) and the frontend
// (WebSocket). It caches the latest catalog the frontend reported so newly
// connected phones and request_catalog commands can be answered immediately,
// fans catalog updates out to catalog subscribers (the BLE notifier and any web
// listeners), and fans phone commands out to command subscribers (the
// frontend). All state is mutex-protected. Create with NewStore; do not copy
// after first use.
type Store struct {
	mu          sync.Mutex
	catalog     []byte // compacted catalog JSON, nil until the frontend reports one
	catalogSubs []*subscriber
	cmdSubs     []*subscriber
}

type subscriber struct {
	ch chan []byte
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return &Store{}
}

// SetCatalog validates and caches a catalog reported by the frontend, then
// notifies catalog subscribers. The payload must be a JSON object; it is
// compacted before storage so BLE notifications carry no whitespace.
func (s *Store) SetCatalog(raw []byte) error {
	if !isJSONObject(raw) {
		return errors.New("headunit: catalog must be a JSON object")
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return err
	}
	compact := buf.Bytes()

	s.mu.Lock()
	s.catalog = compact
	s.mu.Unlock()

	s.fanout(s.catalogSubsSnapshot(), compact)
	return nil
}

// Catalog returns the cached catalog and whether one has been reported.
func (s *Store) Catalog() (catalog []byte, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.catalog == nil {
		return nil, false
	}
	out := make([]byte, len(s.catalog))
	copy(out, s.catalog)
	return out, true
}

// HandleCommand parses and validates a phone command, relays it to command
// subscribers (the frontend), and — for request_catalog — also re-notifies the
// cached catalog to catalog subscribers so the phone gets state immediately
// without waiting for the frontend round-trip.
func (s *Store) HandleCommand(raw []byte) error {
	cmd, err := ParseCommand(raw)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return err
	}
	compact := buf.Bytes()

	s.fanout(s.cmdSubsSnapshot(), compact)

	if cmd.Type == CmdRequestCatalog {
		if catalog, ok := s.Catalog(); ok {
			s.fanout(s.catalogSubsSnapshot(), catalog)
		}
	}
	return nil
}

// SubscribeCatalog returns a channel that receives the full catalog JSON
// whenever it changes (and on request_catalog), plus an unsubscribe function.
// The channel is buffered (size 4); sends are non-blocking (dropped if full).
func (s *Store) SubscribeCatalog() (ch <-chan []byte, unsubscribe func()) {
	return s.subscribe(&s.catalogSubs)
}

// SubscribeCommands returns a channel that receives each phone command JSON,
// plus an unsubscribe function. Same buffering/drop semantics as
// SubscribeCatalog.
func (s *Store) SubscribeCommands() (ch <-chan []byte, unsubscribe func()) {
	return s.subscribe(&s.cmdSubs)
}

func (s *Store) subscribe(list *[]*subscriber) (<-chan []byte, func()) {
	sub := &subscriber{ch: make(chan []byte, 4)}
	s.mu.Lock()
	*list = append(*list, sub)
	s.mu.Unlock()
	return sub.ch, func() {
		s.mu.Lock()
		for i, v := range *list {
			if v == sub {
				*list = append((*list)[:i], (*list)[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
	}
}

func (s *Store) catalogSubsSnapshot() []*subscriber { return s.subsSnapshot(&s.catalogSubs) }
func (s *Store) cmdSubsSnapshot() []*subscriber     { return s.subsSnapshot(&s.cmdSubs) }

func (s *Store) subsSnapshot(list *[]*subscriber) []*subscriber {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*subscriber, len(*list))
	copy(out, *list)
	return out
}

func (s *Store) fanout(subs []*subscriber, msg []byte) {
	for _, sub := range subs {
		// Copy per subscriber: receivers may hold the slice past the next update.
		cp := make([]byte, len(msg))
		copy(cp, msg)
		select {
		case sub.ch <- cp:
		default:
		}
	}
}

// isJSONObject reports whether raw is a syntactically valid JSON object.
func isJSONObject(raw []byte) bool {
	if !json.Valid(raw) {
		return false
	}
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}
