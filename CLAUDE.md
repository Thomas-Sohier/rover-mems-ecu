# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
go build ./...
go build -o rover-mems-agent ./...

# Cross-compile (as CI does)
GOOS=linux GOARCH=arm64 go build -o rover-mems-linux-arm64 ./...
GOOS=linux GOARCH=amd64 go build -o rover-mems-linux-amd64 ./...

# Run with flags
./rover-mems-agent -serialport /dev/ttyUSB0 -ecutype 1.9 -mode debug
```

## Test & Lint

CI (`.github/workflows/main.yml`) runs these before building; run them locally before pushing:

```bash
go test -race -shuffle=on ./...   # unit tests (same flags as CI)
go vet ./...                      # static analysis

# Linter — pinned to v1.64.8 (v2 rejects this repo's v1-style .golangci.yml):
go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8
golangci-lint run
```

`.golangci.yml` enables `errcheck` with `check-type-assertions: true`. Intentionally
ignored serial read/write errors in K-line loops are whitelisted via `issues.exclude-rules`;
add to that list rather than disabling the linter when a serial call's error is deliberately dropped.

## CLI Flags

| Flag | Values | Description |
|------|--------|-------------|
| `-serialport` | e.g. `/dev/ttyUSB0` | Serial port to use (auto-detected if omitted) |
| `-ecutype` | `1.x`, `1.9`, `2J`, `rc5`, `3`, `fake` | ECU variant |
| `-mode` | `prod` (default), `debug` | Enables verbose byte-level logging |
| `-ble` | `true` | Enable the BLE GATT peripheral for the companion phone app |
| `-blename` | `"Rover MEMS"` | BLE local device name advertised to the phone |
| `-capture` | e.g. `/tmp/kline.log` | Append a timestamped trace of every serial transfer to this file |

## Serial capture (`-capture`)

`internal/serial/capture.go` decorates the `Port` returned by `serial.Open`,
appending one line per transfer. It is independent of `-mode debug`: the trace
is for protocol *timing*, the debug log for decoded state, and debug logging to
a slow console distorts the timings the trace exists to measure.

```text
=== open /dev/ttyUSB0 9600 8N1 at 2026-07-26T14:03:11.123456+02:00
       0.000 CFG   read timeout 500ms
     500.104 BRK   low for 400ms requested, 400.312ms actual
    1300.518 BRK   low for 200ms requested, 200.221ms actual
    2001.882 RX    55 12 80
    2033.107 TX    7C
```

Leading column is milliseconds since the header, to microsecond resolution.
Kinds are `TX`, `RX`, `BRK`, `CFG`, `ERR`, `CLOSE`. Timestamps are taken the
instant the underlying call returns, before any formatting, so they record when
a byte arrived rather than when it was written down. Reads that return no bytes
are not traced — the MEMS 1.x loop polls a non-blocking port every 10 ms and
would otherwise bury the traffic.

`BRK` records requested *and* achieved pulse width, which is what tells you
whether the 5-baud wake-up waveform (200 ms bit periods) is holding up on the
target.

## Now-playing / BLE companion

`internal/nowplaying` is a pure Go package (no Bluetooth imports) that parses
the companion phone's BLE write payloads (`ParseMetadata`, `ParseArtControl`,
chunked art reassembly) and stores the current track state in `Store`.
`Store.Subscribe` returns a buffered channel for push notifications; all state
is mutex-protected. `internal/ble` is a thin glue layer that registers a BlueZ
GATT peripheral via `tinygo.org/x/bluetooth` and delegates every write event
to the corresponding `Store.Handle*` method. The web server's
`/api/nowplaying`, `/api/nowplaying/art`, and `/ws/nowplaying` routes read from
the same store.

`internal/navigation` and `internal/notification` are two more pure packages
following the same pattern, for the companion's turn-by-turn navigation stream
(chars `…0005/0006/0007`) and one-shot alert stream (char `…0008`). They are
kept separate on purpose: navigation is a single replacing state (snapshot +
PNG maneuver icon, like now-playing), alerts are fire-once events (no replay to
late subscribers). They expose `/api/navigation`, `/api/navigation/icon`,
`/ws/navigation`, `/api/notifications`, and `/ws/notifications`. See
`docs/companion-notifications-navigation.md`.

`internal/headunit` is the remote-control proxy: the phone lists/switches the
head-unit's views and reads/edits its settings (command char `…0009`, notify
char `…000a`). Unlike the streams above, data flows **both ways** and the agent
is a transparent relay — the on-device frontend is the single source of truth.
The frontend pushes its self-describing catalog over the bidirectional
`/ws/headunit` socket; the agent caches it, fragments it, and notifies the phone
on `…000a`; the phone's commands on `…0009` are relayed back to the frontend.
The catalog notification framing is `[2-byte index][2-byte count][JSON
fragment]`. `/api/headunit` serves the cached catalog. The agent does not
interpret view/setting semantics; the frontend enforces the "current view is
always visible" invariant and re-pushes. `ble.Run` and `web.NewServer` take all
four stores. See `docs/companion-headunit-control.md`.

## Architecture


```text
/ (racine du projet)
├── cmd/
│   └── rover-mems/            # Entrypoint
│       └── main.go            # Init app, read config, launch server and series port.
│
├── internal/                  
│   ├── ecu/                   # ECU 
│   │   ├── ecu.go             # Common INTERFACE for every ECU
│   │   ├── auth.go           
│   │   ├── fake/              # Fake for test purpose
│   │   ├── mems1x/            # shared MEMS 1.X
│   │   ├── mems2j/            # MEMS 2J
│   │   ├── mems3/             # MEMS 3
│   │   └── mems19/            # MEMS 1.9
│   │
│   ├── serial/                # Gestion matérielle des ports séries
│   │   ├── serial.go          
│   │   ├── ports_linux.go     # GO automatically use _linux
│   │   └── ports_windows.go   # GO automatically use _windows
│   │
│   └── web/                   # API and Websocket
│       └── server.go          # Implementation
│
├── pkg/                       # Shared helpers
│   └── utils/
│       └── helpers.go         
│
├── ui/                        # front-end
│   └── dashboard.html         # Always included in //go:embed, but properly placed
│
├── scripts/                   # Build and launch file
│   ├── build_packed.cmd
│   └── run-32.cmd
│
├── go.mod
├── go.sum
├── README.md
└── LICENSE
```

The agent runs a **main event loop** (`main.go`) that retries `connectLoop()` every second. `connectLoop` picks the serial port, then dispatches to the appropriate ECU handler based on `globalEcuType`. All shared state (`globalDataOutput`, `globalFaults`, `globalConnected`, etc.) is protected by `globalDataOutputLock` (a `sync.RWMutex`).

A **Gin HTTP server** (`webserver.go`) runs concurrently. It exposes:
- `GET /api` — JSON snapshot of all ECU data
- `GET /ws` — WebSocket (browser sends `.` to request data, agent responds with full state)
- `GET /ecu/:name`, `/serialPort/:name`, `/command/:name` — runtime configuration

### ECU Handlers

Each ECU type has its own file. The entry point follows the pattern `readFirstBytesFromPort<Type>(fn string)`:

| File | ECU | Baud | Wake-up |
|------|-----|------|---------|
| `ecu-1x.go` + `ecu-1x-shared.go` | MEMS 1.2/1.3/1.6 | 9600 | None — starts init directly |
| `ecu-19.go` | MEMS 1.9 | 9600 | ISO 9141 5-baud (address `0x16`), then `ecu1xLoop` |
| `ecu-2j.go` | MEMS 2J | 10400 | Fast break pulse (25ms), then proprietary framing |
| `ecu-rc5.go` | RC5 (airbag) | 2400 | Custom break pattern |
| `ecu-3.go` | MEMS 3 | — | — |

### K-line / Serial patterns

- **MEMS 1.x and 1.9** share `ecu1xLoop` (in `ecu-1x-shared.go`) for the main data loop. The loop is K-line half-duplex: every sent byte is echoed back, so `ecu1xGotKlineEcho` tracks whether we've consumed our own echo before processing the ECU's response.
- **MEMS 2J** uses a goroutine (`serialReadRoutine` in `serialReadWrite.go`) + a channel (`serialReadChannel`) because Linux serial reads block even with a timeout set. It uses length-prefixed framing with a single XOR/sum checksum.
- `github.com/distributed/sers` is the serial port library. `SetReadParams(minBytes, timeoutSecs)` controls blocking behaviour — `(0, 0.001)` is effectively non-blocking, `(1, 0.5)` blocks up to 500 ms per read.

### ISO 9141 5-baud wake-up (MEMS 1.9)

`send5BaudWakeup` bit-bangs the ECU address `0x16` LSB-first at 5 baud using `SetBreak`. After the stop bit, `handleWakeUpHandshake` waits for the sync byte `0x55` followed by any two keyword bytes (KW1, KW2), then sends `~KW2` as the challenge. `waitForChallengeEcho` accepts either `[~KW2, 0xE9]` or just `[0xE9]` (complement of address), since some K-line interfaces suppress the TX echo.

## Documentation

Documentation can be found online on https://github.com/james-portman/rover-mems-documentation/tree/master.