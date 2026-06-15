// Package navigation implements the head-unit side of the companion phone's
// turn-by-turn navigation stream. Like internal/nowplaying it holds pure
// parsing and store logic with no Bluetooth imports; the GATT server in
// internal/ble delegates writes to it.
//
// Wire format summary (characteristics live under the same service UUID as
// now-playing, 7f3a0001-…):
//
//	Navigation char (7f3a0005-…): UTF-8 JSON written whenever the current
//	instruction changes. Fields: active (bool), instruction, distance, eta,
//	maneuver_icon_id (each string-or-null). When active is false the head unit
//	must clear its navigation display; the other fields are then null.
//
//	Maneuver-icon-control char (7f3a0006-…): UTF-8 JSON announcing an upcoming
//	icon upload. Fields: maneuver_icon_id, total_bytes, chunk_count.
//
//	Maneuver-icon-data char (7f3a0007-…): binary chunks written without
//	response. Layout: 2-byte big-endian chunk index followed by PNG payload
//	bytes (PNG, not JPEG — the maneuver arrow is transparency-bearing so the
//	head unit can recolor it). Reassembled by index like cover art. The phone
//	deduplicates by id, only re-sending when maneuver_icon_id changes.
package navigation

// NavCharUUID is the write characteristic UUID for navigation-state JSON.
const NavCharUUID = "7f3a0005-9c44-4e6b-8d2a-5b1f00000001"

// IconControlCharUUID is the write characteristic UUID for maneuver-icon
// upload announcements.
const IconControlCharUUID = "7f3a0006-9c44-4e6b-8d2a-5b1f00000001"

// IconDataCharUUID is the write-without-response characteristic UUID for
// chunked maneuver-icon PNG data.
const IconDataCharUUID = "7f3a0007-9c44-4e6b-8d2a-5b1f00000001"
