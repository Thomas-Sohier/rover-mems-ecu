// Package headunit implements the head-unit side of the companion phone's
// remote-control protocol. Like internal/nowplaying it holds pure parsing,
// framing and store logic with no Bluetooth imports; the GATT server in
// internal/ble delegates to it and the web server bridges it to the frontend.
//
// The head unit is the single source of truth for its views and settings: the
// on-device frontend (the dashboard / RPi Flutter UI) owns that state and
// pushes a self-describing catalog to this agent over /ws/headunit. This agent
// is a transparent proxy — it caches the latest catalog, relays it to the
// phone over BLE notifications, and relays the phone's commands back to the
// frontend. It does not interpret view/setting semantics or enforce the
// "current view is always visible" invariant; the frontend does that and
// re-pushes the resulting catalog.
//
// Wire format summary (characteristics live under the now-playing service UUID
// 7f3a0001-…):
//
//	Command char (7f3a0009-…): UTF-8 JSON written by the phone. One of:
//	  {"type":"set_current_view","view_id":…}
//	  {"type":"set_view_visibility","view_id":…,"visible":true|false}
//	  {"type":"set_setting_value","setting_id":…,"value":bool|number|string}
//	  {"type":"request_catalog"}
//	Fire-and-forget; no application-level ack. Authoritative state always comes
//	back on the catalog characteristic.
//
//	Catalog char (7f3a000a-…): notify-only. The agent pushes the full catalog
//	JSON {"views":[…],"settings":[…]} on a request_catalog command and whenever
//	the frontend reports a change. A catalog can exceed one notification, so it
//	is fragmented: each notification is
//	  bytes 0..1  chunk index   (uint16 big-endian, 0-based)
//	  bytes 2..3  chunk count   (uint16 big-endian, total fragments)
//	  bytes 4..   UTF-8 JSON fragment
//	The phone reassembles fragments 0..count-1 in order.
package headunit

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// CommandCharUUID is the write characteristic UUID for phone → head-unit
// control commands.
const CommandCharUUID = "7f3a0009-9c44-4e6b-8d2a-5b1f00000001"

// CatalogCharUUID is the notify characteristic UUID for the head-unit → phone
// catalog.
const CatalogCharUUID = "7f3a000a-9c44-4e6b-8d2a-5b1f00000001"

// CatalogHeaderBytes is the per-fragment header size: a 2-byte big-endian chunk
// index followed by a 2-byte big-endian chunk count.
const CatalogHeaderBytes = 4

// MaxFragmentPayload bounds the UTF-8 payload bytes carried by one catalog
// notification. The phone negotiates MTU 517 immediately after connecting, so
// 180 stays well under the usable notification size (MTU-3-4) while keeping
// fragment counts low for typical catalogs.
const MaxFragmentPayload = 180

// Command kinds understood on the command characteristic.
const (
	CmdSetCurrentView    = "set_current_view"
	CmdSetViewVisibility = "set_view_visibility"
	CmdSetSettingValue   = "set_setting_value"
	CmdRequestCatalog    = "request_catalog"
)

// Command is a decoded control command from the phone. Value is left as raw
// JSON because its type depends on the target setting (bool, number, or string)
// and this agent only relays it.
type Command struct {
	Type      string          `json:"type"`
	ViewID    string          `json:"view_id,omitempty"`
	Visible   *bool           `json:"visible,omitempty"`
	SettingID string          `json:"setting_id,omitempty"`
	Value     json.RawMessage `json:"value,omitempty"`
}

// ParseCommand decodes and validates a command-characteristic write payload.
func ParseCommand(data []byte) (Command, error) {
	var c Command
	if err := json.Unmarshal(data, &c); err != nil {
		return Command{}, fmt.Errorf("headunit: parse command: %w", err)
	}
	switch c.Type {
	case CmdSetCurrentView:
		if c.ViewID == "" {
			return Command{}, fmt.Errorf("headunit: %s missing view_id", c.Type)
		}
	case CmdSetViewVisibility:
		if c.ViewID == "" {
			return Command{}, fmt.Errorf("headunit: %s missing view_id", c.Type)
		}
		if c.Visible == nil {
			return Command{}, fmt.Errorf("headunit: %s missing visible", c.Type)
		}
	case CmdSetSettingValue:
		if c.SettingID == "" {
			return Command{}, fmt.Errorf("headunit: %s missing setting_id", c.Type)
		}
		if len(c.Value) == 0 {
			return Command{}, fmt.Errorf("headunit: %s missing value", c.Type)
		}
	case CmdRequestCatalog:
		// no fields
	default:
		return Command{}, fmt.Errorf("headunit: unknown command type %q", c.Type)
	}
	return c, nil
}

// BuildFrames splits a catalog JSON document into notification frames of at most
// maxPayload payload bytes each, prefixed with a 2-byte big-endian chunk index
// and a 2-byte big-endian chunk count. It mirrors the phone's reassembler. At
// least one frame is always returned, even for an empty document.
func BuildFrames(catalog []byte, maxPayload int) [][]byte {
	if maxPayload <= 0 {
		panic("headunit: maxPayload must be positive")
	}
	count := (len(catalog) + maxPayload - 1) / maxPayload
	if count == 0 {
		count = 1
	}
	frames := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		start := i * maxPayload
		end := start + maxPayload
		if end > len(catalog) {
			end = len(catalog)
		}
		var payload []byte
		if start < len(catalog) {
			payload = catalog[start:end]
		}
		frame := make([]byte, CatalogHeaderBytes+len(payload))
		binary.BigEndian.PutUint16(frame[0:2], uint16(i))
		binary.BigEndian.PutUint16(frame[2:4], uint16(count))
		copy(frame[CatalogHeaderBytes:], payload)
		frames = append(frames, frame)
	}
	return frames
}
