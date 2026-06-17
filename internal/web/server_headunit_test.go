package web

import (
	"context"
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

func newHeadUnitTestServer(hu *headunit.Store) *Server {
	return NewServer(ecu.NewState(), nowplaying.NewStore(), navigation.NewStore(), notification.NewStore(), hu)
}

func TestAPIHeadUnit_EmptyIs404(t *testing.T) {
	srv := newHeadUnitTestServer(headunit.NewStore())
	router := srv.buildRouter(context.Background())

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/headunit", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404", w.Code)
	}
}

// TestWSHeadUnit_FrontendCatalogCachedAndCommandRelayed exercises the full
// bridge: the frontend pushes a catalog over /ws/headunit (cached, served by
// /api/headunit), and a phone command (via the store) is relayed to the
// frontend socket.
func TestWSHeadUnit_FrontendCatalogCachedAndCommandRelayed(t *testing.T) {
	hu := headunit.NewStore()
	srv := newHeadUnitTestServer(hu)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ts := httptest.NewServer(srv.buildRouter(ctx))
	defer ts.Close()

	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/headunit"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Frontend pushes its catalog.
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"views":[{"id":"home"}],"settings":[]}`)); err != nil {
		t.Fatal(err)
	}

	// It becomes available on /api/headunit (compacted).
	deadline := time.Now().Add(2 * time.Second)
	var body string
	for time.Now().Before(deadline) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/api/headunit", nil)
		srv.buildRouter(ctx).ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			body = w.Body.String()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if body != `{"views":[{"id":"home"}],"settings":[]}` {
		t.Fatalf("api/headunit body = %q", body)
	}

	// A phone command is relayed to the frontend socket.
	if err := hu.HandleCommand([]byte(`{"type":"set_current_view","view_id":"home"}`)); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read relayed command: %v", err)
	}
	if string(msg) != `{"type":"set_current_view","view_id":"home"}` {
		t.Fatalf("relayed command = %q", msg)
	}
}
