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

There are no tests in this repository.

## CLI Flags

| Flag | Values | Description |
|------|--------|-------------|
| `-serialport` | e.g. `/dev/ttyUSB0` | Serial port to use (auto-detected if omitted) |
| `-ecutype` | `1.x`, `1.9`, `2J`, `rc5`, `3`, `fake` | ECU variant |
| `-mode` | `prod` (default), `debug` | Enables verbose byte-level logging |

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