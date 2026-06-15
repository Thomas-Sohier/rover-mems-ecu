package notification

import (
	"testing"
	"time"
)

func TestParseAlert_Valid(t *testing.T) {
	a, err := ParseAlert([]byte(`{"app":"Signal","title":"Alice","text":"Hi","posted_at":1700000000000}`))
	if err != nil {
		t.Fatal(err)
	}
	if a.App != "Signal" || a.Title != "Alice" || a.Text != "Hi" || a.PostedAt != 1700000000000 {
		t.Fatalf("unexpected: %+v", a)
	}
}

func TestParseAlert_InvalidJSON(t *testing.T) {
	if _, err := ParseAlert([]byte("not json")); err == nil {
		t.Fatal("expected error")
	}
}

func TestStore_Last(t *testing.T) {
	s := NewStore()
	if _, ok := s.Last(); ok {
		t.Fatal("expected no last alert initially")
	}
	if err := s.HandleAlert([]byte(`{"app":"Mail","title":"T","text":"X","posted_at":1}`)); err != nil {
		t.Fatal(err)
	}
	a, ok := s.Last()
	if !ok || a.App != "Mail" {
		t.Fatalf("Last: ok=%v alert=%+v", ok, a)
	}
}

func TestStore_Subscribe_ReceivesAlerts(t *testing.T) {
	s := NewStore()
	ch, unsub := s.Subscribe()
	defer unsub()
	if err := s.HandleAlert([]byte(`{"app":"Mail","title":"T","text":"X","posted_at":1}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-ch:
		if a.Title != "T" {
			t.Fatalf("unexpected alert: %+v", a)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for alert")
	}
}

// A late subscriber must not receive alerts that were posted before it
// subscribed: alerts are fire-once events, not replayed state.
func TestStore_Subscribe_NoReplayForLateJoiner(t *testing.T) {
	s := NewStore()
	if err := s.HandleAlert([]byte(`{"app":"Mail","title":"old","text":"","posted_at":1}`)); err != nil {
		t.Fatal(err)
	}
	ch, unsub := s.Subscribe()
	defer unsub()
	select {
	case a := <-ch:
		t.Fatalf("unexpected replayed alert: %+v", a)
	case <-time.After(100 * time.Millisecond):
		// expected: nothing delivered
	}
}
