package navigation

import (
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"
)

// --- ParseNavigation ---

func TestParseNavigation_Full(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"active":           true,
		"instruction":      "Turn right onto Rue de Rivoli",
		"distance":         "200 m",
		"eta":              "14:32",
		"maneuver_icon_id": "abc12345",
	})
	n, err := ParseNavigation(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !n.Active || n.Instruction != "Turn right onto Rue de Rivoli" {
		t.Fatalf("unexpected fields: %+v", n)
	}
	if n.Distance != "200 m" || n.Eta != "14:32" || n.IconID != "abc12345" {
		t.Fatalf("unexpected fields: %+v", n)
	}
}

func TestParseNavigation_NullFieldsCleared(t *testing.T) {
	raw := []byte(`{"active":false,"instruction":null,"distance":null,"eta":null,"maneuver_icon_id":null}`)
	n, err := ParseNavigation(raw)
	if err != nil {
		t.Fatal(err)
	}
	if n.Active {
		t.Fatal("expected Active=false")
	}
	if n.Instruction != "" || n.Distance != "" || n.Eta != "" || n.IconID != "" {
		t.Fatalf("expected empty fields, got %+v", n)
	}
}

func TestParseNavigation_InvalidJSON(t *testing.T) {
	if _, err := ParseNavigation([]byte("not json")); err == nil {
		t.Fatal("expected error")
	}
}

// --- ParseIconControl ---

func TestParseIconControl_Valid(t *testing.T) {
	id, total, count, err := ParseIconControl([]byte(`{"maneuver_icon_id":"i1","total_bytes":500,"chunk_count":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if id != "i1" || total != 500 || count != 2 {
		t.Fatalf("got %q %d %d", id, total, count)
	}
}

// --- Store: navigation + icon reassembly ---

func TestStore_HandleNavigation_Snapshot(t *testing.T) {
	s := NewStore()
	if err := s.HandleNavigation([]byte(`{"active":true,"instruction":"Go","distance":"1 km","eta":"10:00","maneuver_icon_id":"i1"}`)); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if !snap.Navigation.Active || snap.Navigation.Instruction != "Go" {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	// Icon referenced but not yet received.
	if snap.HasIcon {
		t.Fatal("expected HasIcon=false before icon arrives")
	}
}

func iconChunk(idx int, payload []byte) []byte {
	b := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(b[:2], uint16(idx))
	copy(b[2:], payload)
	return b
}

func TestStore_IconReassembly_FlipsHasIcon(t *testing.T) {
	s := NewStore()
	if err := s.HandleNavigation([]byte(`{"active":true,"instruction":"Go","distance":"","eta":"","maneuver_icon_id":"i1"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleIconControl([]byte(`{"maneuver_icon_id":"i1","total_bytes":4,"chunk_count":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleIconChunk(iconChunk(0, []byte{1, 2})); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().HasIcon {
		t.Fatal("HasIcon should be false until all chunks received")
	}
	if err := s.HandleIconChunk(iconChunk(1, []byte{3, 4})); err != nil {
		t.Fatal(err)
	}
	id, png, ok := s.Icon()
	if !ok || id != "i1" {
		t.Fatalf("Icon: ok=%v id=%q", ok, id)
	}
	if string(png) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("reassembled icon mismatch: %v", png)
	}
	if !s.Snapshot().HasIcon {
		t.Fatal("expected HasIcon=true after reassembly")
	}
}

func TestStore_IconChunkOverflow(t *testing.T) {
	s := NewStore()
	if err := s.HandleIconControl([]byte(`{"maneuver_icon_id":"i1","total_bytes":2,"chunk_count":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleIconChunk(iconChunk(0, []byte{1, 2, 3})); err == nil {
		t.Fatal("expected overflow error")
	}
}

func TestStore_IconChunkWithoutControl(t *testing.T) {
	s := NewStore()
	if err := s.HandleIconChunk(iconChunk(0, []byte{1})); err == nil {
		t.Fatal("expected error when no transfer in progress")
	}
}

func TestStore_Subscribe_NotifiesOnNavigation(t *testing.T) {
	s := NewStore()
	ch, unsub := s.Subscribe()
	defer unsub()
	if err := s.HandleNavigation([]byte(`{"active":true,"instruction":"Go","distance":"","eta":"","maneuver_icon_id":null}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case snap := <-ch:
		if snap.Navigation.Instruction != "Go" {
			t.Fatalf("unexpected: %+v", snap)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for snapshot")
	}
}
