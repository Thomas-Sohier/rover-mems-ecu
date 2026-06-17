# Companion head-unit control — implementation notes

These notes document the head-unit side of the companion phone's **remote
control** feature: the phone lists the head-unit's display views, switches the
current view, toggles view visibility, and reads/edits the head-unit's settings.

Unlike now-playing, navigation, and alerts — where the phone writes and the head
unit only consumes — control data flows **both ways**, and the agent
(`internal/headunit`) is a **transparent proxy**. The on-device frontend (the
dashboard / RPi Flutter UI) is the single source of truth: it owns the catalog
of views and settings, applies commands, enforces invariants, and re-publishes.
The agent caches the latest catalog and relays bytes in each direction.

```
phone  ──cmd  (BLE …0009 write)──▶  agent ──cmd──▶  frontend (/ws/headunit)
phone  ◀─catalog (BLE …000a notify)─ agent ◀─catalog─ frontend (/ws/headunit)
```

## GATT protocol

Characteristics live under the existing companion service UUID
`7f3a0001-9c44-4e6b-8d2a-5b1f00000001`.

| Characteristic | UUID | Flags | Payload |
|---|---|---|---|
| Head-unit command | `7f3a0009-…` | write | UTF-8 JSON, one of `set_current_view` / `set_view_visibility` / `set_setting_value` / `request_catalog` (see below). Fire-and-forget; no application-level ack. |
| Head-unit catalog | `7f3a000a-…` | notify | Fragmented catalog JSON. Each notification is `[2-byte index][2-byte count][UTF-8 fragment]` (both integers big-endian). The phone reassembles fragments `0..count-1` in order. |

### Commands (phone → head unit)

```json
{"type":"set_current_view","view_id":"map"}
{"type":"set_view_visibility","view_id":"trip","visible":false}
{"type":"set_setting_value","setting_id":"brightness","value":60}
{"type":"request_catalog"}
```

`value` is a bool, number, or string depending on the target setting's type. The
agent validates the command shape (`ParseCommand`) and relays it verbatim; it
does not interpret the meaning.

### Catalog (head unit → phone)

A self-describing document the frontend publishes:

```json
{
  "views": [
    {"id":"home","label":"Home","visible":true,"current":true},
    {"id":"map","label":"Map","visible":true,"current":false}
  ],
  "settings": [
    {"id":"night_mode","label":"Night mode","type":"bool","value":false},
    {"id":"theme","label":"Theme","type":"enum","value":"dark",
     "options":[{"value":"dark","label":"Dark"},{"value":"light","label":"Light"}]},
    {"id":"brightness","label":"Brightness","type":"number","value":60,
     "min":0,"max":100,"step":5}
  ]
}
```

`type` ∈ `bool | enum | number`. The agent stores the catalog as compacted JSON
(no whitespace) to minimise BLE bytes; it does not validate the inner schema
beyond "is a JSON object".

## Initial push & the request_catalog handshake

BlueZ (via `tinygo.org/x/bluetooth`) exposes no "central subscribed" callback,
so the agent cannot push proactively when the phone enables notifications.
Instead the phone sends `request_catalog` after subscribing. On that command the
agent (a) relays it to the frontend so the frontend re-publishes authoritative
state, and (b) immediately re-notifies its cached catalog (if any) so the phone
gets state without waiting for the frontend round-trip.

## Frontend bridge — `/ws/headunit`

A single bidirectional WebSocket. The frontend:

- **sends** its catalog (a JSON object) as a text message whenever it changes;
  the agent caches it and notifies the phone;
- **receives** each phone command as a text message; it applies the command,
  updates its catalog, and sends the new catalog back.

`GET /api/headunit` returns the cached catalog (404 until the frontend has
published one).

## Invariants the frontend owns (not the agent)

- The current view is always visible. When a `set_view_visibility … false`
  hides the current view, the frontend switches the current view to another
  visible one before re-publishing.
- Commands are advisory: the phone does not assume success until the catalog
  reflects the change. The agent therefore never needs to ack.

## Code layout

- `internal/headunit/protocol.go` — UUIDs, `ParseCommand`, `BuildFrames`
  (catalog fragmentation, mirrors the phone's reassembler).
- `internal/headunit/headunit.go` — `Store`: catalog cache, command/catalog
  fan-out, `request_catalog` re-notify.
- `internal/ble/server.go` — command write char + catalog notify char +
  `runCatalogNotifier` goroutine that fragments and writes notifications.
- `internal/web/server.go` — `/api/headunit` and the `/ws/headunit` bridge.
