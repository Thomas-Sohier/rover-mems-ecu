// Package notification implements the head-unit side of the companion phone's
// alert stream: one-shot notifications forwarded from a user-allowlisted app
// (messages, emails, …). Like internal/nowplaying it holds pure parsing and
// store logic with no Bluetooth imports; the GATT server in internal/ble
// delegates writes to it.
//
// It is intentionally separate from internal/navigation: alerts are fire-once
// events, whereas navigation is a single continuously-replacing state.
//
// Wire format summary (characteristic lives under the same service UUID as
// now-playing, 7f3a0001-…):
//
//	Alert char (7f3a0008-…): UTF-8 JSON written once when a notification is
//	posted. Fields: app (display label), title, text, posted_at (Unix epoch
//	ms). The phone never forwards updates or dismissals, so each write is an
//	independent event; the head unit displays and expires it as it sees fit.
package notification

// AlertCharUUID is the write characteristic UUID for one-shot alert JSON.
const AlertCharUUID = "7f3a0008-9c44-4e6b-8d2a-5b1f00000001"
