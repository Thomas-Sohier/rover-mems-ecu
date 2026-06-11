// Package nowplaying implements the head-unit side of the companion phone BLE
// protocol. It holds the pure parsing and store logic with no Bluetooth
// imports; the actual GATT server lives in internal/ble.
//
// Wire format summary:
//
//	Service UUID: 7f3a0001-9c44-4e6b-8d2a-5b1f00000001
//
//	Metadata char (7f3a0002-…): UTF-8 JSON written by the phone whenever the
//	track or playback state changes. Fields: title, artist, album, state
//	(playing|paused|stopped), position_ms, duration_ms, art_id (string or null).
//
//	Art-control char (7f3a0003-…): UTF-8 JSON that announces an upcoming art
//	upload. Fields: art_id, total_bytes, chunk_count.
//
//	Art-data char (7f3a0004-…): binary chunks written without response.
//	Layout: 2-byte big-endian chunk index followed by the JPEG payload bytes.
//	Chunks may arrive out of order; the store reassembles them by index once
//	total_bytes have been received.
package nowplaying

// ServiceUUID is the GATT service UUID advertised by the head-unit.
const ServiceUUID = "7f3a0001-9c44-4e6b-8d2a-5b1f00000001"

// MetadataCharUUID is the write characteristic UUID for track metadata JSON.
const MetadataCharUUID = "7f3a0002-9c44-4e6b-8d2a-5b1f00000001"

// ArtControlCharUUID is the write characteristic UUID for art-upload
// announcements.
const ArtControlCharUUID = "7f3a0003-9c44-4e6b-8d2a-5b1f00000001"

// ArtDataCharUUID is the write-without-response characteristic UUID for
// chunked art data.
const ArtDataCharUUID = "7f3a0004-9c44-4e6b-8d2a-5b1f00000001"
