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

func TestAPIStateConsumesAlertError(t *testing.T) {
	state := ecu.NewState()
	state.Alert = "test-alert"
	state.Error = "test-error"
	srv := newAPITestServer(state)

	w := do(t, srv, http.MethodGet, "/api")
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["alert"] != "test-alert" || got["error"] != "test-error" {
		t.Fatalf("first read: alert=%v error=%v", got["alert"], got["error"])
	}

	// Alert/Error are one-shot: a second read must see them cleared.
	w2 := do(t, srv, http.MethodGet, "/api")
	var got2 map[string]any
	if err := json.Unmarshal(w2.Body.Bytes(), &got2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got2["alert"] != "" || got2["error"] != "" {
		t.Fatalf("second read not cleared: alert=%v error=%v", got2["alert"], got2["error"])
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
