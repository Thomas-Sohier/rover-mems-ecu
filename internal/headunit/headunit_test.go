package headunit

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// --- ParseCommand ---

func TestParseCommand_Valid(t *testing.T) {
	cases := []string{
		`{"type":"set_current_view","view_id":"map"}`,
		`{"type":"set_view_visibility","view_id":"map","visible":false}`,
		`{"type":"set_setting_value","setting_id":"bright","value":60}`,
		`{"type":"set_setting_value","setting_id":"theme","value":"dark"}`,
		`{"type":"request_catalog"}`,
		`{"type":"nav_key","key":"next"}`,
		`{"type":"nav_key","key":"ok"}`,
		`{"type":"nav_key","key":"back"}`,
	}
	for _, c := range cases {
		if _, err := ParseCommand([]byte(c)); err != nil {
			t.Errorf("ParseCommand(%s) unexpected error: %v", c, err)
		}
	}
}

func TestParseCommand_Invalid(t *testing.T) {
	cases := []string{
		`not json`,
		`{"type":"bogus"}`,
		`{"type":"set_current_view"}`, // missing view_id
		`{"type":"set_view_visibility","view_id":"map"}`, // missing visible
		`{"type":"set_setting_value","setting_id":"x"}`,  // missing value
		`{"type":"set_setting_value","value":1}`,         // missing setting_id
		`{"type":"nav_key"}`,                             // missing key
		`{"type":"nav_key","key":"select"}`,              // unknown key
	}
	for _, c := range cases {
		if _, err := ParseCommand([]byte(c)); err == nil {
			t.Errorf("ParseCommand(%s) expected error, got nil", c)
		}
	}
}

func TestParseCommand_VisiblePreserved(t *testing.T) {
	cmd, err := ParseCommand([]byte(`{"type":"set_view_visibility","view_id":"map","visible":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Visible == nil || *cmd.Visible {
		t.Fatalf("expected visible=false, got %v", cmd.Visible)
	}
}

// --- BuildFrames ---

func TestBuildFrames_SingleFrame(t *testing.T) {
	doc := []byte(`{"views":[]}`)
	frames := BuildFrames(doc, 100)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}
	if got := binary.BigEndian.Uint16(frames[0][0:2]); got != 0 {
		t.Errorf("index = %d, want 0", got)
	}
	if got := binary.BigEndian.Uint16(frames[0][2:4]); got != 1 {
		t.Errorf("count = %d, want 1", got)
	}
	if !bytes.Equal(frames[0][CatalogHeaderBytes:], doc) {
		t.Errorf("payload mismatch")
	}
}

func TestBuildFrames_EmptyStillOneFrame(t *testing.T) {
	frames := BuildFrames(nil, 100)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}
	if got := binary.BigEndian.Uint16(frames[0][2:4]); got != 1 {
		t.Errorf("count = %d, want 1", got)
	}
}

func TestBuildFrames_ReassemblesInOrder(t *testing.T) {
	doc := bytes.Repeat([]byte("abcdefghij"), 20) // 200 bytes
	frames := BuildFrames(doc, 16)
	if len(frames) < 2 {
		t.Fatalf("expected multiple frames, got %d", len(frames))
	}
	var got []byte
	for i, f := range frames {
		if idx := binary.BigEndian.Uint16(f[0:2]); int(idx) != i {
			t.Fatalf("frame %d has index %d", i, idx)
		}
		if cnt := binary.BigEndian.Uint16(f[2:4]); int(cnt) != len(frames) {
			t.Fatalf("frame %d has count %d, want %d", i, cnt, len(frames))
		}
		got = append(got, f[CatalogHeaderBytes:]...)
	}
	if !bytes.Equal(got, doc) {
		t.Errorf("reassembled payload mismatch")
	}
}

// --- Store ---

func TestStore_SetCatalogCachesCompactedAndNotifies(t *testing.T) {
	s := NewStore()
	ch, unsub := s.SubscribeCatalog()
	defer unsub()

	if err := s.SetCatalog([]byte(`{ "views": [ ] , "settings": [] }`)); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-ch:
		if string(got) != `{"views":[],"settings":[]}` {
			t.Errorf("notified %q, want compacted form", got)
		}
	default:
		t.Fatal("expected a catalog notification")
	}

	cached, ok := s.Catalog()
	if !ok || string(cached) != `{"views":[],"settings":[]}` {
		t.Errorf("cached = %q ok=%v", cached, ok)
	}
}

func TestStore_SetCatalogRejectsNonObject(t *testing.T) {
	s := NewStore()
	if err := s.SetCatalog([]byte(`[1,2,3]`)); err == nil {
		t.Error("expected error for JSON array")
	}
	if err := s.SetCatalog([]byte(`nope`)); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestStore_HandleCommandRelaysToCommandSubscribers(t *testing.T) {
	s := NewStore()
	ch, unsub := s.SubscribeCommands()
	defer unsub()

	if err := s.HandleCommand([]byte(`{ "type": "set_current_view", "view_id": "map" }`)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ch:
		if string(got) != `{"type":"set_current_view","view_id":"map"}` {
			t.Errorf("relayed %q", got)
		}
	default:
		t.Fatal("expected command relay")
	}
}

func TestStore_RequestCatalogReNotifiesCachedCatalog(t *testing.T) {
	s := NewStore()
	if err := s.SetCatalog([]byte(`{"views":[{"id":"home"}]}`)); err != nil {
		t.Fatal(err)
	}

	catCh, unsub1 := s.SubscribeCatalog()
	defer unsub1()
	cmdCh, unsub2 := s.SubscribeCommands()
	defer unsub2()

	if err := s.HandleCommand([]byte(`{"type":"request_catalog"}`)); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-catCh:
		if string(got) != `{"views":[{"id":"home"}]}` {
			t.Errorf("re-notified %q", got)
		}
	default:
		t.Fatal("expected cached catalog re-notification")
	}
	// request_catalog is also relayed to the frontend so it can re-push.
	select {
	case <-cmdCh:
	default:
		t.Fatal("expected request_catalog to be relayed to frontend")
	}
}

func TestStore_HandleCommandRejectsInvalid(t *testing.T) {
	s := NewStore()
	if err := s.HandleCommand([]byte(`{"type":"bogus"}`)); err == nil {
		t.Error("expected error for unknown command")
	}
}

func TestStore_UnsubscribeStopsDelivery(t *testing.T) {
	s := NewStore()
	ch, unsub := s.SubscribeCatalog()
	unsub()
	if err := s.SetCatalog([]byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
		t.Error("received after unsubscribe")
	default:
	}
}
