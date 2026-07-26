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
	"rover-mems-agent/internal/headunit"
	"rover-mems-agent/internal/navigation"
	"rover-mems-agent/internal/notification"
	"rover-mems-agent/internal/nowplaying"

	"github.com/gorilla/websocket"
)

func TestAPINewPlaying_EmptySnapshot(t *testing.T) {
	state := ecu.NewState()
	np := nowplaying.NewStore()
	srv := NewServer(state, np, navigation.NewStore(), notification.NewStore(), headunit.NewStore())

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

	srv := NewServer(state, np, navigation.NewStore(), notification.NewStore(), headunit.NewStore())
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

func TestCheckOrigin(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		want   bool
	}{
		{"no origin (non-browser client)", "", true},
		{"localhost", "http://localhost:8080", true},
		{"localhost no port", "http://localhost", true},
		{"loopback v4", "http://127.0.0.1:8080", true},
		{"loopback v6", "http://[::1]:8080", true},
		{"https localhost", "https://localhost:8080", true},

		{"foreign origin", "http://evil.example", false},
		{"foreign origin naming us in path", "http://evil.example/localhost", false},
		// The classic bypass: a hostname that merely starts with localhost.
		{"prefix lookalike", "http://localhost.evil.example", false},
		{"loopback lookalike", "http://127.0.0.1.evil.example", false},
		{"null origin", "null", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/ws", nil)
			r.Host = "localhost:8080"
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}

			if got := checkOrigin(r); got != tc.want {
				t.Errorf("checkOrigin(Origin: %q) = %v, want %v", tc.origin, got, tc.want)
			}
		})
	}
}

// TestCheckOriginIgnoresHost is the point of the change: a hostile page can set
// the Host header to ours simply by pointing its WebSocket at us, so Host must
// not be what grants access.
func TestCheckOriginIgnoresHost(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/ws", nil)
	r.Host = "localhost:8080"
	r.Header.Set("Origin", "http://evil.example")

	if checkOrigin(r) {
		t.Error("a localhost Host let a foreign Origin through")
	}
}
