package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rover-mems-agent/internal/ecu"
	"rover-mems-agent/internal/nowplaying"

	"github.com/gorilla/websocket"
)

func TestAPINewPlaying_EmptySnapshot(t *testing.T) {
	state := ecu.NewState()
	np := nowplaying.NewStore()
	srv := NewServer(state, np)

	gin := srv.buildRouter(context.Background())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/nowplaying", nil)
	gin.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	var snap nowplaying.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if snap.HasArt {
		t.Fatal("expected HasArt=false")
	}
}

func TestWSNewPlaying_InitialAndPushed(t *testing.T) {
	state := ecu.NewState()
	np := nowplaying.NewStore()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := NewServer(state, np)
	ts := httptest.NewServer(srv.buildRouter(ctx))
	defer ts.Close()

	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/nowplaying"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Should receive initial snapshot immediately.
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read initial: %v", err)
	}
	var snap nowplaying.Snapshot
	if err := json.Unmarshal(msg, &snap); err != nil {
		t.Fatalf("unmarshal initial: %v", err)
	}

	// Trigger a metadata update; should receive a pushed snapshot.
	raw, _ := json.Marshal(map[string]any{
		"title": "PushedTrack", "artist": "", "album": "", "state": "playing",
		"position_ms": 0, "duration_ms": 0, "art_id": nil,
	})
	if err := np.HandleMetadata(raw); err != nil {
		t.Fatal(err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, msg2, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read pushed: %v", err)
	}
	var snap2 nowplaying.Snapshot
	if err := json.Unmarshal(msg2, &snap2); err != nil {
		t.Fatalf("unmarshal pushed: %v", err)
	}
	if snap2.Metadata.Title != "PushedTrack" {
		t.Fatalf("unexpected title: %q", snap2.Metadata.Title)
	}
}
