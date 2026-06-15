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
	"rover-mems-agent/internal/navigation"
	"rover-mems-agent/internal/notification"
	"rover-mems-agent/internal/nowplaying"

	"github.com/gorilla/websocket"
)

func newTestServer(nav *navigation.Store, notif *notification.Store) *Server {
	return NewServer(ecu.NewState(), nowplaying.NewStore(), nav, notif)
}

func TestAPINavigation_EmptySnapshot(t *testing.T) {
	srv := newTestServer(navigation.NewStore(), notification.NewStore())
	router := srv.buildRouter(context.Background())

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/navigation", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	var snap navigation.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if snap.Navigation.Active || snap.HasIcon {
		t.Fatalf("expected empty snapshot, got %+v", snap)
	}
}

func TestAPINavigationIcon_NotFoundWhenEmpty(t *testing.T) {
	srv := newTestServer(navigation.NewStore(), notification.NewStore())
	router := srv.buildRouter(context.Background())

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/navigation/icon", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404", w.Code)
	}
}

func TestAPINotifications_NotFoundWhenEmpty(t *testing.T) {
	srv := newTestServer(navigation.NewStore(), notification.NewStore())
	router := srv.buildRouter(context.Background())

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/notifications", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404", w.Code)
	}
}

func TestWSNavigation_InitialAndPushed(t *testing.T) {
	nav := navigation.NewStore()
	srv := newTestServer(nav, notification.NewStore())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ts := httptest.NewServer(srv.buildRouter(ctx))
	defer ts.Close()

	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/navigation"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Initial snapshot on connect.
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read initial: %v", err)
	}

	if err := nav.HandleNavigation([]byte(`{"active":true,"instruction":"Turn left","distance":"","eta":"","maneuver_icon_id":null}`)); err != nil {
		t.Fatal(err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read pushed: %v", err)
	}
	var snap navigation.Snapshot
	if err := json.Unmarshal(msg, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if snap.Navigation.Instruction != "Turn left" {
		t.Fatalf("unexpected instruction: %q", snap.Navigation.Instruction)
	}
}

func TestWSNotifications_PushedAlert(t *testing.T) {
	notif := notification.NewStore()
	srv := newTestServer(navigation.NewStore(), notif)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ts := httptest.NewServer(srv.buildRouter(ctx))
	defer ts.Close()

	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/notifications"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// No initial snapshot for fire-once alerts; the next read is the alert.
	if err := notif.HandleAlert([]byte(`{"app":"Signal","title":"Alice","text":"Hi","posted_at":1}`)); err != nil {
		t.Fatal(err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read alert: %v", err)
	}
	var alert notification.Alert
	if err := json.Unmarshal(msg, &alert); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if alert.App != "Signal" || alert.Title != "Alice" {
		t.Fatalf("unexpected alert: %+v", alert)
	}
}
