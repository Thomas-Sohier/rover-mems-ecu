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

// newAPITestServer builds a Server around a caller-supplied ecu.State so tests
// can inspect the state the config routes mutate, with empty companion stores.
func newAPITestServer(state *ecu.State) *Server {
	return NewServer(state, nowplaying.NewStore(), navigation.NewStore(), notification.NewStore(), headunit.NewStore())
}

// do routes one request through the built router and returns the recorder.
func do(t *testing.T, srv *Server, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(method, target, nil)
	srv.buildRouter(context.Background()).ServeHTTP(w, req)
	return w
}

func TestAPIState(t *testing.T) {
	state := ecu.NewState()
	state.Data["rpm"] = 850

	w := do(t, newAPITestServer(state), http.MethodGet, "/api")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}

	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"faults", "connected", "ecuType", "ecuData", "agentVersion"} {
		if _, ok := got[k]; !ok {
			t.Errorf("payload missing key %q", k)
		}
	}
	if got["connected"] != false {
		t.Errorf("connected: got %v want false", got["connected"])
	}
	ecuData, ok := got["ecuData"].(map[string]any)
	if !ok || ecuData["rpm"] != float64(850) {
		t.Errorf("ecuData.rpm: got %v want 850", got["ecuData"])
	}
}

// TestAPIStateReportsAlertErrorToEveryReader replaces an earlier test that
// asserted /api cleared Alert/Error as it read them.
//
// That one-shot behaviour was the bug: /api and /ws both consumed the same
// fields, so whichever polled first swallowed the notice and the other never
// saw it. Notices now persist and carry a sequence number, and it is the client
// that shows each one once, per bump.
func TestAPIStateReportsAlertErrorToEveryReader(t *testing.T) {
	state := ecu.NewState()
	state.SetAlert("test-alert")
	state.SetError("test-error")
	srv := newAPITestServer(state)

	read := func() map[string]any {
		t.Helper()
		w := do(t, srv, http.MethodGet, "/api")
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return got
	}

	first := read()
	if first["alert"] != "test-alert" || first["error"] != "test-error" {
		t.Fatalf("first read: alert=%v error=%v", first["alert"], first["error"])
	}

	// A second reader (in production, the WebSocket stream) must see them too.
	second := read()
	if second["alert"] != "test-alert" || second["error"] != "test-error" {
		t.Fatalf("second reader lost the notice: alert=%v error=%v", second["alert"], second["error"])
	}
	if second["alertSeq"] != first["alertSeq"] || second["errorSeq"] != first["errorSeq"] {
		t.Errorf("sequence changed without a new notice: %v/%v then %v/%v",
			first["alertSeq"], first["errorSeq"], second["alertSeq"], second["errorSeq"])
	}

	// Raising the same text again is still distinguishable as a new notice.
	state.SetAlert("test-alert")
	if third := read(); third["alertSeq"] == first["alertSeq"] {
		t.Errorf("alertSeq did not advance on a repeated alert: %v", third["alertSeq"])
	}
}

func TestAPIPorts(t *testing.T) {
	state := ecu.NewState()
	state.SerialPorts = []string{"/dev/ttyUSB0", "/dev/ttyUSB1"}
	state.SelectedSerialPort = "/dev/ttyUSB0"

	w := do(t, newAPITestServer(state), http.MethodGet, "/api/ports")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
	var got struct {
		Ports    []string `json:"ports"`
		Selected string   `json:"selected"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Ports) != 2 || got.Selected != "/dev/ttyUSB0" {
		t.Fatalf("got %+v", got)
	}
}

func TestConnectedEndpoint(t *testing.T) {
	state := ecu.NewState()
	state.Connected = true

	w := do(t, newAPITestServer(state), http.MethodGet, "/connected")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
	var got struct {
		Connected bool `json:"connected"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Connected {
		t.Fatal("connected: got false want true")
	}
}

func TestFaultsEndpoint(t *testing.T) {
	state := ecu.NewState()
	state.Faults = []string{"coolant_sensor", "throttle_pot"}

	w := do(t, newAPITestServer(state), http.MethodGet, "/faults")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
	var got struct {
		Faults []string `json:"faults"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Faults) != 2 || got.Faults[0] != "coolant_sensor" {
		t.Fatalf("got %+v", got.Faults)
	}
}

func TestPing(t *testing.T) {
	w := do(t, newAPITestServer(ecu.NewState()), http.MethodGet, "/ping")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "pong") {
		t.Fatalf("body: got %q", w.Body.String())
	}
}

func TestSetEcuType(t *testing.T) {
	state := ecu.NewState()
	w := do(t, newAPITestServer(state), http.MethodPost, "/ecu/1.9")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
	if got := state.Snapshot().EcuType; got != "1.9" {
		t.Fatalf("EcuType: got %q want 1.9", got)
	}
}

func TestSetUserCommand(t *testing.T) {
	state := ecu.NewState()
	w := do(t, newAPITestServer(state), http.MethodPost, "/command/clearfaults")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
	if got := state.Snapshot().UserCommand; got != "clearfaults" {
		t.Fatalf("UserCommand: got %q want clearfaults", got)
	}
}

func TestSetSerialPort_Success(t *testing.T) {
	state := ecu.NewState()
	// The port is a full device path whose slashes must survive as a query value.
	w := do(t, newAPITestServer(state), http.MethodPost, "/serialPort?name=%2Fdev%2FttyUSB0")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
	if got := state.Snapshot().SelectedSerialPort; got != "/dev/ttyUSB0" {
		t.Fatalf("SelectedSerialPort: got %q want /dev/ttyUSB0", got)
	}
}

func TestSetSerialPort_MissingName(t *testing.T) {
	state := ecu.NewState()
	w := do(t, newAPITestServer(state), http.MethodPost, "/serialPort")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", w.Code)
	}
	if got := state.Snapshot().SelectedSerialPort; got != "" {
		t.Fatalf("SelectedSerialPort should be unchanged, got %q", got)
	}
}

func TestWSMain_RespondsToDot(t *testing.T) {
	state := ecu.NewState()
	state.Connected = true
	state.Data["rpm"] = 900

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ts := httptest.NewServer(newAPITestServer(state).buildRouter(ctx))
	defer ts.Close()

	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(".")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(msg, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["connected"] != true {
		t.Errorf("connected: got %v want true", got["connected"])
	}
	// timestamp must be RFC3339 so the browser's new Date() can parse it.
	tsStr, ok := got["timestamp"].(string)
	if !ok {
		t.Fatalf("timestamp missing or not a string: %v", got["timestamp"])
	}
	if _, err := time.Parse(time.RFC3339, tsStr); err != nil {
		t.Fatalf("timestamp %q not RFC3339: %v", tsStr, err)
	}
}
