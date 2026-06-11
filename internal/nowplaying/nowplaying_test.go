package nowplaying

import (
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"
)

// --- ParseMetadata ---

func TestParseMetadata_Full(t *testing.T) {
	artID := "abc123"
	raw, _ := json.Marshal(map[string]any{
		"title": "Track", "artist": "Band", "album": "Album",
		"state": "playing", "position_ms": int64(1000), "duration_ms": int64(240000),
		"art_id": artID,
	})
	m, err := ParseMetadata(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "Track" || m.Artist != "Band" || m.Album != "Album" {
		t.Fatalf("unexpected fields: %+v", m)
	}
	if m.State != "playing" || m.PositionMs != 1000 || m.DurationMs != 240000 {
		t.Fatalf("unexpected numeric fields: %+v", m)
	}
	if m.ArtID != artID {
		t.Fatalf("art_id: got %q want %q", m.ArtID, artID)
	}
}

func TestParseMetadata_NullArtID(t *testing.T) {
	raw := []byte(`{"title":"T","artist":"A","album":"","state":"paused","position_ms":0,"duration_ms":0,"art_id":null}`)
	m, err := ParseMetadata(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.ArtID != "" {
		t.Fatalf("expected empty ArtID, got %q", m.ArtID)
	}
}

func TestParseMetadata_InvalidJSON(t *testing.T) {
	_, err := ParseMetadata([]byte("not json"))
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- ParseArtControl ---

func TestParseArtControl_Valid(t *testing.T) {
	raw := []byte(`{"art_id":"img1","total_bytes":500,"chunk_count":2}`)
	id, total, count, err := ParseArtControl(raw)
	if err != nil {
		t.Fatal(err)
	}
	if id != "img1" || total != 500 || count != 2 {
		t.Fatalf("unexpected: id=%q total=%d count=%d", id, total, count)
	}
}

func TestParseArtControl_InvalidJSON(t *testing.T) {
	_, _, _, err := ParseArtControl([]byte("{bad"))
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Store art reassembly ---

func makeChunk(idx uint16, payload []byte) []byte {
	b := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(b[:2], idx)
	copy(b[2:], payload)
	return b
}

func artControl(artID string, totalBytes, chunkCount int) []byte {
	b, _ := json.Marshal(map[string]any{
		"art_id": artID, "total_bytes": totalBytes, "chunk_count": chunkCount,
	})
	return b
}

func TestStore_ArtReassembly_InOrder(t *testing.T) {
	s := NewStore()
	chunk0 := []byte("AAAA")
	chunk1 := []byte("BBBB")
	total := len(chunk0) + len(chunk1)

	if err := s.HandleArtControl(artControl("id1", total, 2)); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleArtChunk(makeChunk(0, chunk0)); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleArtChunk(makeChunk(1, chunk1)); err != nil {
		t.Fatal(err)
	}

	id, jpeg, ok := s.Art()
	if !ok {
		t.Fatal("expected art")
	}
	if id != "id1" {
		t.Fatalf("art id: got %q", id)
	}
	if string(jpeg) != "AAAABBBB" {
		t.Fatalf("jpeg content: %q", string(jpeg))
	}
}

func TestStore_ArtReassembly_OutOfOrder(t *testing.T) {
	s := NewStore()
	chunk0 := []byte("AAAA")
	chunk1 := []byte("BBBB")
	total := len(chunk0) + len(chunk1)

	if err := s.HandleArtControl(artControl("id2", total, 2)); err != nil {
		t.Fatal(err)
	}
	// send chunk 1 before chunk 0
	if err := s.HandleArtChunk(makeChunk(1, chunk1)); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleArtChunk(makeChunk(0, chunk0)); err != nil {
		t.Fatal(err)
	}

	_, jpeg, ok := s.Art()
	if !ok {
		t.Fatal("expected art")
	}
	if string(jpeg) != "AAAABBBB" {
		t.Fatalf("jpeg content: %q", string(jpeg))
	}
}

func TestStore_ChunkBeforeControl_Error(t *testing.T) {
	s := NewStore()
	err := s.HandleArtChunk(makeChunk(0, []byte("data")))
	if err == nil {
		t.Fatal("expected error when no transfer in progress")
	}
}

func TestStore_Overflow_Error(t *testing.T) {
	s := NewStore()
	// Declare totalBytes=2 but send 5 bytes.
	if err := s.HandleArtControl(artControl("id3", 2, 1)); err != nil {
		t.Fatal(err)
	}
	err := s.HandleArtChunk(makeChunk(0, []byte("XXXXX")))
	if err == nil {
		t.Fatal("expected overflow error")
	}
	// Transfer should be reset: subsequent chunk should error too.
	err2 := s.HandleArtChunk(makeChunk(0, []byte("X")))
	if err2 == nil {
		t.Fatal("expected error after transfer reset")
	}
}

func TestStore_NewControl_DiscardsPartial(t *testing.T) {
	s := NewStore()
	// Start a transfer but don't finish it.
	if err := s.HandleArtControl(artControl("old", 100, 5)); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleArtChunk(makeChunk(0, []byte("partial"))); err != nil {
		t.Fatal(err)
	}
	// New control overwrites.
	chunk := []byte("FULL")
	if err := s.HandleArtControl(artControl("new", len(chunk), 1)); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleArtChunk(makeChunk(0, chunk)); err != nil {
		t.Fatal(err)
	}
	id, jpeg, ok := s.Art()
	if !ok {
		t.Fatal("expected art")
	}
	if id != "new" || string(jpeg) != "FULL" {
		t.Fatalf("unexpected art id=%q content=%q", id, jpeg)
	}
}

// --- Subscribe ---

func TestSubscribe_MetadataUpdate(t *testing.T) {
	s := NewStore()
	ch, unsub := s.Subscribe()
	defer unsub()

	raw, _ := json.Marshal(map[string]any{
		"title": "X", "artist": "", "album": "", "state": "playing",
		"position_ms": 0, "duration_ms": 0, "art_id": nil,
	})
	if err := s.HandleMetadata(raw); err != nil {
		t.Fatal(err)
	}

	select {
	case snap := <-ch:
		if snap.Metadata.Title != "X" {
			t.Fatalf("unexpected title: %q", snap.Metadata.Title)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for snapshot")
	}
}

func TestSubscribe_CompletedArt_HasArt(t *testing.T) {
	s := NewStore()
	ch, unsub := s.Subscribe()
	defer unsub()

	data := []byte("JPEG")
	if err := s.HandleArtControl(artControl("img", len(data), 1)); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleArtChunk(makeChunk(0, data)); err != nil {
		t.Fatal(err)
	}

	select {
	case snap := <-ch:
		if !snap.HasArt {
			t.Fatal("expected HasArt=true")
		}
		if snap.ArtID != "img" {
			t.Fatalf("unexpected art id: %q", snap.ArtID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for snapshot")
	}
}

func TestSubscribe_Unsubscribe_StopsDelivery(t *testing.T) {
	s := NewStore()
	ch, unsub := s.Subscribe()
	unsub()

	raw, _ := json.Marshal(map[string]any{
		"title": "Y", "artist": "", "album": "", "state": "stopped",
		"position_ms": 0, "duration_ms": 0, "art_id": nil,
	})
	if err := s.HandleMetadata(raw); err != nil {
		t.Fatal(err)
	}

	select {
	case <-ch:
		t.Fatal("should not receive after unsubscribe")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestSubscribe_FullBuffer_DoesNotBlock(t *testing.T) {
	s := NewStore()
	_, unsub := s.Subscribe() // subscribe but never drain
	defer unsub()

	raw, _ := json.Marshal(map[string]any{
		"title": "Z", "artist": "", "album": "", "state": "playing",
		"position_ms": 0, "duration_ms": 0, "art_id": nil,
	})
	// Send more than buffer size (8) — must not block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			_ = s.HandleMetadata(raw)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("HandleMetadata blocked on full subscriber channel")
	}
}
