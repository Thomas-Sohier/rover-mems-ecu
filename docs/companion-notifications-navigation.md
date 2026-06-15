# Companion notifications & navigation — implementation notes

These notes document the head-unit side of the companion phone's two newer BLE
streams: **navigation** (turn-by-turn) and **notifications/alerts** (one-shot
forwarded notifications). They sit alongside the existing now-playing stream and
follow the same architecture: pure parsing/store packages with no Bluetooth
imports (`internal/navigation`, `internal/notification`), a thin GATT glue layer
(`internal/ble`), and HTTP/WebSocket read endpoints (`internal/web`).

The two streams are deliberately **separate concepts**:

- **Navigation** is a single, continuously-replacing state. Each write replaces
  the current instruction; the phone throttles distance/ETA-only updates and
  sends `active:false` to clear the display when navigation ends.
- An **alert** is a fire-once event (a message, an email). The phone sends it
  once on post and never forwards updates or dismissals.

So navigation has a *current snapshot* (like now-playing) while alerts are an
*event stream* with no meaningful "current state" beyond "the most recent one".

## GATT protocol

All characteristics live under the existing companion service UUID
`7f3a0001-9c44-4e6b-8d2a-5b1f00000001`. The phone is the GATT client and only
writes.

| Characteristic | UUID | Flags | Payload |
|---|---|---|---|
| Navigation | `7f3a0005-…` | write | UTF-8 JSON `{"active","instruction","distance","eta","maneuver_icon_id"}`. `active` is a bool; the four others are string-or-`null`. `active:false` clears the display (other fields `null`). |
| Maneuver-icon control | `7f3a0006-…` | write | UTF-8 JSON `{"maneuver_icon_id","total_bytes","chunk_count"}` — announces an upcoming icon upload. |
| Maneuver-icon data | `7f3a0007-…` | write without response | Binary: 2-byte big-endian chunk index + **PNG** payload bytes. Reassembled by index like cover art. |
| Alert | `7f3a0008-…` | write | UTF-8 JSON `{"app","title","text","posted_at"}`. `app` is the display label; `posted_at` is Unix epoch ms. |

Notes:

- The maneuver icon is **PNG, not JPEG** — the phone derives it from the
  navigation notification's monochrome small-icon and keeps alpha so the head
  unit can recolor the arrow. Treat it as a transparency-bearing image.
- The phone **deduplicates the icon by `maneuver_icon_id`** and only re-sends the
  control+data writes when the maneuver changes. The navigation JSON keeps
  referencing the same id between turns, so the icon persists until replaced.
- Ordering: the phone writes the navigation JSON first (referencing an
  `maneuver_icon_id`), then the icon control + data chunks. So a navigation
  snapshot can name an icon that has not been fully received yet — mirror the
  now-playing `art_id`/`has_art` two-phase behaviour with `icon_id`/`has_icon`.
- **What Google Maps actually provides** is limited to localized display
  strings (`instruction`, `distance`, `eta`) plus the rendered arrow bitmap.
  There is no structured maneuver type, numeric distance, or coordinates — that
  data only exists in the private Android-Auto API. Do not expect to parse
  semantics out of `instruction`; render it as text.

## Packages

### `internal/navigation`

- `ParseNavigation`, `ParseIconControl` — pure decoders.
- `Store` — holds the current `Navigation` plus the reassembled maneuver-icon
  PNG. `HandleNavigation`, `HandleIconControl`, `HandleIconChunk` are the GATT
  write entry points (same chunk-reassembly logic as now-playing art).
  `Snapshot()` returns `{navigation, icon_id, has_icon}`; `Icon()` returns the
  PNG bytes; `Subscribe()` returns a buffered channel of snapshots.
- `has_icon` is true only when the stored icon's id matches the id referenced by
  the current navigation, so a stale icon is never reported against a new turn.

### `internal/notification`

- `ParseAlert` — pure decoder.
- `Store` — fans each alert out to subscribers and retains the most recent one
  for a point-in-time read. **Past alerts are not replayed to late joiners**:
  they are events, not state. `HandleAlert` is the GATT write entry point;
  `Last()` returns the most recent alert; `Subscribe()` returns a buffered
  channel of alerts.

### `internal/ble`

`Run(ctx, npStore, navStore, notifStore, deviceName)` registers all eight
characteristics under the single companion service and delegates each write to
the corresponding `Handle*` method. Adding the new streams did not change the
service UUID or advertising.

## HTTP / WebSocket endpoints

Read paths mirror now-playing. JSON over WS; binary icon over a separate REST
endpoint (you cannot stream PNG bytes inside the JSON snapshot).

| Endpoint | Description |
|---|---|
| `GET /api/navigation` | Current navigation snapshot `{navigation, icon_id, has_icon}`. |
| `GET /api/navigation/icon` | Raw `image/png` of the current maneuver icon, or `404` if none. |
| `GET /ws/navigation` | WebSocket. Sends the current snapshot on connect, then pushes on every change. Fetch the icon from `/api/navigation/icon` when `has_icon` flips true / `icon_id` changes. |
| `GET /api/notifications` | The most recent alert, or `404` if none yet. |
| `GET /ws/notifications` | WebSocket. **No initial snapshot** (alerts are fire-once); pushes each alert posted while connected. |

Both WebSocket routes are listen-only: the server keeps them alive with
periodic pings, extends the read deadline on pong, and closes when the peer goes
away or the server context is cancelled (60 s idle timeout). They share the
`wsReaderDone`/`wsPing`/`wsJSONWrite` helpers in `internal/web/server.go`.

## Testing

- `internal/navigation` and `internal/notification` have pure-Go unit tests for
  parsing, chunk reassembly (including overflow / no-transfer errors), the
  `has_icon` matching rule, and the no-replay-for-late-joiners alert rule.
- `internal/web` has `httptest` coverage for the REST 404/empty cases and the
  two WebSocket streams (initial+pushed for navigation, pushed alert for
  notifications).
- BLE itself is not unit-tested (no device); `internal/ble` stays a thin glue
  layer so all logic is covered in the pure packages.
