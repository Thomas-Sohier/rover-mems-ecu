# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Scope and limitations

This is a personal project, developed and tested against **one** setup: a
Raspberry Pi 3B+ driving a VAG 409.1 KKL OBD cable (assumed FTDI FT232RL — the
listing also named CH340T and the exact chip is unconfirmed). Everything outside
that combination is inferred from public protocol documentation and has not been
verified on hardware. It may well not work on yours.

Concretely, these are the things that differ between setups and that the code
cannot detect on its own:

- **TX echo.** The K-line is single-wire half-duplex and most interfaces echo
  what we transmit back onto RX. `MEMS1x.ReadData` hardcodes that assumption,
  which is right for an L9637D-based cable like the KKL. An adapter that
  suppresses the echo would have every ECU reply silently eaten, because in
  MEMS 1.x the acknowledgement *is* the command byte and the two are
  indistinguishable by value.
- **Break support.** The 5-baud and fast-init wake-ups need `TIOCSBRK`. Adapters
  whose kernel driver has no `break_ctl` cannot wake an ECU at all.
- **Buffering and latency.** FTDI's 16 ms default latency timer eats most of the
  25–50 ms ISO 9141 W4 window; CH340 buffers differently. Timing margins that
  hold on one adapter can fail on another.
- **Framing-error byte values.** Drivers report a framing error as `0x00`,
  `0xF8`, `0xFE` or other junk, with no portable rule. The wake-up parsing
  tolerates all of it on purpose.
- **Load.** The bit-banged 5-baud waveform uses `time.Sleep` for its 200 ms bit
  periods. A loaded or thermally throttled Pi can stretch them. On the target
  setup they held to within 1.5 ms (see the measured figures below), but that is
  one idle Pi, not a guarantee.

Where the code is deliberately permissive — searching for a sync byte rather
than expecting it first, accepting several shapes of acknowledgement — that
tolerance is there to absorb this variance. Do not tighten it to match one
adapter's observed behaviour.

**Per-variant status.** MEMS 1.9 works on the car. A 104 s capture on
2026-07-27 (`assets/mems19.log`) covers wake-up, init, 895 polling cycles and a
real engine start, with no framing error and no malformed frame — see
"Reference capture" below for what it establishes. MEMS 1.x shares the same poll
loop and is therefore exercised by the same run from `0xCA` onwards, but its
own no-wake-up entry path has never been tried. MEMS 2J, 3 and RC5 have unit
tests covering their parsers and state machines, but unit tests exercise a
scripted fake port with no notion of elapsed time: a green run says the decoding
is self-consistent, never that the protocol works on a real car. The `fake` ECU
type bypasses the wire protocol entirely and only exercises the web layer. Use
`-capture` (below) for anything that matters on the wire.

## Build & Run

```bash
go build ./...
go build -o rover-mems-agent ./cmd/rover-mems

# Cross-compile (as CI does). A Pi 3B+ on 32-bit Raspberry Pi OS reports
# armv7l and runs neither of the other two — check `uname -m` before deploying.
GOOS=linux GOARCH=arm64 go build -o rover-mems-linux-arm64 ./cmd/rover-mems
GOOS=linux GOARCH=amd64 go build -o rover-mems-linux-amd64 ./cmd/rover-mems
GOOS=linux GOARCH=arm GOARM=7 go build -o rover-mems-linux-arm ./cmd/rover-mems

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

`-race` needs cgo and a C toolchain, so it does not run on a bare Windows
checkout; CI covers it on Linux. Run `go test -shuffle=on ./...` locally and say
so rather than claiming the race detector passed.

`.golangci.yml` enables `errcheck` with `check-type-assertions: true` and
currently carries **no** exclude-rules. If a serial call's error is genuinely
meant to be dropped, add an `issues.exclude-rules` entry with the reason rather
than disabling the linter — but prefer propagating it: every past exclusion here
turned out to be hiding a real failure mode.

Run `golangci-lint` with `GOOS=linux` when working on Windows; `ports_linux.go`
is otherwise never compiled and its findings are silently skipped.

## CLI Flags

| Flag | Values | Description |
|------|--------|-------------|
| `-serialport` | e.g. `/dev/ttyUSB0` | Serial port to use (auto-detected if omitted) |
| `-ecutype` | `1.x`, `1.9`, `2J`, `rc5`, `3`, `fake` | ECU variant |
| `-mode` | `prod` (default), `debug` | Enables verbose byte-level logging |
| `-port` | `8080` (default) | HTTP server port |
| `-ble` | `true` (default) | Enable the BLE GATT peripheral for the companion phone app |
| `-blename` | `"Rover MEMS"` | BLE local device name advertised to the phone |
| `-capture` | e.g. `/tmp/kline.log` | Append a timestamped trace of every serial transfer to this file |

## Architecture

```text
cmd/rover-mems/main.go        # flags, event loop, graceful shutdown
internal/
  ecu/
    ecu.go                    # State (shared runtime data) + ECU interface + registry
    auth.go                   # GenerateKey: seed/key security access (2J and 3)
    fake/                     # synthetic data, no wire protocol — for UI work
    mems1x/                   # MEMS 1.2/1.3/1.6; loop.go is shared with 1.9
    mems19/                   # MEMS 1.9: 5-baud wake-up, then delegates to mems1x
    mems2j/                   # MEMS 2J (mems2j.go, parse.go, faults.go)
    mems3/                    # MEMS 3
    rc5/                      # RC5 airbag ECU
    serialtest/               # FakePort: scriptable serial.Port for tests
  serial/
    port.go                   # Port interface + Open
    capture.go                # -capture tracing decorator
    readwrite.go              # Reader: goroutine + channel non-blocking reads (2J)
    ports_{linux,windows}.go  # port enumeration, chosen by build tag
  web/
    server.go                 # Gin router, REST + WebSocket
    dashboard.html            # embedded via //go:embed
  ble/                        # BlueZ GATT peripheral (tinygo.org/x/bluetooth)
  bluetooth/                  # BlueZ pairing agent (godbus)
  nowplaying/ navigation/ notification/ headunit/   # companion-phone stores
  logging/                    # stdlib log -> daily file + stderr
  wifi/                       # rfkill wrapper
docs/                         # companion protocol specs
```

`main.go` runs an event loop that calls `connectLoop()` with **exponential
backoff** (`minRetryDelay` 1 s, doubling to `maxRetryDelay` 30 s; reset on a
successful attempt). A cancelled context is a shutdown, not a failure, and does
not inflate the backoff. `connectLoop` picks the serial port, then
`ecu.Factory` builds the handler for the configured type.

All shared ECU data lives in `*ecu.State`, passed explicitly — there are no
package-level globals.

### The ECU registry

`ecu.ECU` is the interface every variant implements: `Connect(ctx, portName)`,
`ReadData(ctx)`, `Close()`, `Type()`. Implementations register themselves from
`init()`:

```go
func init() { ecu.Register("1.9", NewMEMS19) }
```

and `cmd/rover-mems/main.go` blank-imports each package to trigger it. **To add
an ECU**: new package under `internal/ecu/`, implement the interface, `Register`
in `init()`, add the blank import. Nothing else dispatches on ECU type.

| Type | Package | Baud | Wake-up |
|------|---------|------|---------|
| `1.x` | `mems1x` | 9600 8N1 | none — sends `0xCA` directly |
| `1.9` | `mems19` | 9600 8N1 | ISO 9141 5-baud slow-init (address `0x16`), then the `mems1x` loop |
| `2J` | `mems2j` | 10400 8N1 | 25 ms break pulse (fast init), then start-communication |
| `3` | `mems3` | 9600 **8E1** | none — `1A 9A` init command, ECU replies `5A 9A` |
| `rc5` | `rc5` | 2400 8N1 | scripted break pattern |
| `fake` | `fake` | — | none; fills `State.Data` with synthetic values and never touches a port |

`fake` bypasses the wire protocol entirely. It exercises the web layer, **not**
the K-line state machines — do not treat a passing `fake` run as protocol
coverage.

## `ecu.State` — invariants worth knowing

`State` is the one piece of shared mutable state, and several of its properties
look like bugs until you know why they are there. Read this before changing it.

**Two mutexes, deliberately.** `mu` guards the ECU data; `logMu` guards
`LogLines`/`logSeq`. ECU parsers log while holding `mu`, and `sync.RWMutex` is
not reentrant, so a single mutex self-deadlocks on every debug-mode run. Nothing
may take `mu` while holding `logMu`, or the reverse.

**Alerts and errors are not consumed by reading.** `SetAlert`/`SetError` bump
`alertSeq`/`errorSeq`; consumers report a notice when the sequence *changes*.
Clearing the field on read looks tidier but means `/api` and `/ws` steal each
other's notices — whichever polls first wins and the other never sees it. The
client decides when a notice has been shown.

**`Snapshot` deliberately omits the log lines.** They are the largest field by
far and only the WebSocket stream wants them. Streaming consumers call
`LogLinesSince(cursor)`, which returns only what is new and clamps both stale
and future cursors.

**Cheap accessors exist for a reason.** `IsConnected()` and `FaultsCopy()` avoid
the four container copies a full `Snapshot` performs; use them on hot paths.

## K-line / serial patterns

`go.bug.st/serial` is the serial library, wrapped by our own `serial.Port`
interface so ECU packages and their tests do not depend on it directly.

- `SetReadTimeout(0)` makes `Read` non-blocking (returns whatever is buffered);
  a positive duration blocks up to that long. A timed-out read returns
  `(0, nil)`, not an error.
- `Break(d)` asserts the line low for `d` and clears it, in one blocking call
  (`TIOCSBRK` + sleep + `TIOCCBRK` on Linux). It **fails on adapters whose
  driver has no `break_ctl`** — always propagate that error, since without a
  break there is no wake-up and everything read afterwards is noise.

**MEMS 1.x and 1.9** share `MEMS1x.loop` (`mems1x/loop.go`). The K-line is
single-wire half-duplex: every byte we transmit is echoed back, so
`m.gotKlineEcho` / `m.lastKlineByte` track whether we have consumed our own echo
before treating anything as a reply. Note the ambiguity this rests on — the
ECU's acknowledgement *is* the command byte, so echo and reply are
indistinguishable by value. `ReadData` hardcodes echo mode on; that is correct
for an L9637D-based KKL cable but would silently eat every reply on an adapter
that suppresses the TX echo.

**MEMS 2J** uses `serial.Reader` (goroutine + buffered channel) because Linux
serial reads block even with a timeout set. Length-prefixed framing, single
XOR/sum checksum. Its command state machine is a derived transition table
(`buildNextAfterPoll`), not a chain of conditionals.

**Package-level command literals must be copied before `append`.** `sendKey`,
`requestHeader` and friends are package vars with zero spare capacity;
`append(sendKey, x)` happens to allocate today, but any change to the literal
makes it write through the shared backing array. Three real bugs came from this.

### ISO 9141 5-baud wake-up (MEMS 1.9)

`send5BaudWakeup` bit-bangs address `0x16` LSB-first at 5 baud (200 ms per bit)
by coalescing runs of logic-0 into one `Break` and runs of logic-1 into one
sleep. The frame is start bit + 8 data bits + stop bit; `0x16` happens to be
valid both as 8N1 and as 7 data bits with odd parity, so the waveform suits
either ECU convention.

`handleWakeUpHandshake` then *searches* the buffer for the sync byte `0x55`
followed by two keyword bytes and replies `~KW2`. It searches rather than
expecting `0x55` first because our own echoed break arrives as framing errors,
which drivers report as `0x00`, `0xF8`, `0xFE` or other junk depending on the
adapter. `waitForChallengeEcho` likewise accepts `0xE9` anywhere in the reply.
The handshake is best-effort: `Connect` proceeds to the `0xCA` init either way.

Timing constants are named and commented in `mems19.go`: `w4Delay` (30 ms, in
the ISO 25–50 ms window) and `p3Delay` (60 ms before the first request).

Measured on the target setup (`assets/mems19.log`), the waveform is three
`Break` calls of 400/200/600 ms — `start+d0` low, `d1 d2` high, `d3` low, `d4`
high, `d5 d6 d7` low — and it lands within 1.5 ms of nominal on every edge:

| Nominal | Achieved |
|---------|----------|
| 400 ms low | 400.899 ms |
| 400 ms high | 401.0 ms |
| 200 ms low | 201.288 ms |
| 200 ms high | 200.8 ms |
| 600 ms low | 601.390 ms |

The ECU answered `55 76 83`; W4 came out at 30.4 ms and P3 at 60.7 ms, so both
constants are right as written. Three `0x00` framing-error bytes preceded the
`0x55` — the concrete case the search-don't-expect rule above exists for.

## Serial capture (`-capture`)

`internal/serial/capture.go` decorates the `Port` returned by `serial.Open`,
appending one line per transfer. Use it **instead of** `-mode debug`, not
alongside: the trace is for protocol *timing*, the debug log for decoded state,
and debug logging to a slow console distorts the very timings being measured.

```text
=== open /dev/ttyUSB0 9600 8N1 at 2026-07-26T14:03:11.123456+02:00
       0.000 CFG   read timeout 500ms
     500.104 BRK   low for 400ms requested, 400.312ms actual
    2001.882 RX    55 12 80
    2033.107 TX    7C
```

Leading column is milliseconds since the header, to microsecond resolution.
Kinds are `TX`, `RX`, `BRK`, `CFG`, `ERR`, `CLOSE`. Timestamps are taken the
instant the underlying call returns, before any formatting. Reads that returned
no bytes are not traced — the MEMS 1.x loop polls every 10 ms and would
otherwise bury the traffic. `BRK` records achieved pulse width alongside
requested, which is the direct measure of whether the 5-baud waveform holds up
on the target.

On FTDI adapters, lower the latency timer first: the chip holds short packets
for 16 ms by default, which is most of the 25–50 ms handshake window.

```bash
echo 1 | sudo tee /sys/bus/usb-serial/devices/ttyUSB0/latency_timer
```

### Reference capture

`assets/mems19.log` is a known-good MEMS 1.9 run kept as a regression baseline:
104 s, wake-up through `CLOSE`, engine started at 50.7 s and stopped at 92.5 s.
Compare against it before blaming the wire for a decoding problem.

What it establishes:

- **Framing never drifts.** 448 `0x80` polls returned exactly 30 RX bytes each
  (echo + ack + a 28-byte frame, `packetSize` 0x1C) and 447 `0x7D` polls exactly
  35 (echo + ack + 33, `packetSize` 0x21). No short frame, no resync, no `ERR`
  line. A poll pair takes ~214 ms, so the dashboard refreshes at ~4.7 Hz.
- **The values track physical reality.** Across the start: battery sags to 10.8 V
  on the starter then climbs to 13.5 V on the alternator; MAP drops from 101 kPa
  to ~34 kPa as manifold vacuum builds; RPM peaks at 1631 then settles to a
  888–940 idle; coolant climbs 79 → 85 °C. Both DTC bytes stayed `0x00`.

Two decoding notes this run pinned down, worth knowing before you trust a field:

- **`ignition_switch` is a raw byte, not a boolean.** `parseData7D` stores
  `data[1]` as-is, and on this car it reads `0x40` — so a consumer testing it for
  1 sees 64 and a consumer testing for non-zero sees "on" even when it is not.
  It held `0x40` for 413 of 447 frames and only dropped to `0x00` *after* the
  engine stopped, then bounced back to `0x40`, so it is not a simple ignition
  on/off flag. Which bit means what is unconfirmed — do not rename or reinterpret
  it without a capture that isolates the signal.
- **Compare mid-run, not first-frame-to-last.** Both ends of this capture are
  engine-off, so a naive diff of the two shows a battery that mysteriously gained
  0.7 V (really just surface charge after the alternator ran) and hides the start
  entirely.

## HTTP / WebSocket API

`internal/web/server.go`, Gin. CORS allows `http://localhost` and
`http://127.0.0.1` only.

| Route | Purpose |
|-------|---------|
| `GET /` | embedded dashboard |
| `GET /ping`, `/connected`, `/faults` | health and quick state |
| `GET /api` | JSON snapshot of all ECU data |
| `GET /api/ports` | discovered serial ports + selection |
| `GET /api/nowplaying`, `/api/nowplaying/art` | companion track state, cover art |
| `GET /api/navigation`, `/api/navigation/icon` | companion navigation state, PNG icon |
| `GET /api/notifications`, `/api/headunit` | companion alerts, cached view catalog |
| `POST /ecu/:name`, `/command/:name` | runtime configuration |
| `POST /serialPort?name=…` | **query param**, not a path segment — the value contains slashes |
| `POST /wifi/enable`, `/wifi/disable` | `rfkill` via `internal/wifi` |
| `GET /ws` | main stream; browser sends `.`, agent replies with full state |
| `GET /ws/nowplaying`, `/ws/navigation`, `/ws/notifications` | companion streams |
| `GET /ws/headunit` | **bidirectional**: frontend pushes its catalog, phone commands come back |

**`checkOrigin` validates the `Origin` header, not `r.Host`.** Checking `Host`
compares our own hostname against itself, which is always true — that is a
cross-site WebSocket hijacking hole wearing the costume of a check. Requests
with no `Origin` (non-browser clients) are allowed; browser origins must resolve
to localhost.

Each `/ws` connection keeps its own `logCursor` so two clients cannot consume
each other's log lines.

## Companion phone (BLE)

`internal/nowplaying`, `navigation`, `notification` and `headunit` are pure Go
packages with **no Bluetooth imports**: they parse the phone's BLE write
payloads and hold state, all mutex-protected, exposing a `Subscribe`-style
method that returns a buffered channel plus its unsubscribe func (`headunit`
has two, `SubscribeCatalog` and `SubscribeCommands`, since it relays both
ways). `internal/ble` is the thin glue that registers the
BlueZ GATT peripheral via `tinygo.org/x/bluetooth` and delegates every write to
the matching `Store.Handle*` method. `ble.Run` and `web.NewServer` both take all
four stores. Keeping the parsing free of Bluetooth is what makes it testable —
preserve that split.

They are separate packages on purpose:

- **now-playing / navigation** are single replacing states (snapshot + an image:
  cover art, PNG maneuver icon). Chunked image transfers are bounded
  (`MaxArtBytes`, `MaxArtChunks`) and expire (`artTransferTTL`) — the input is
  untrusted and a partial transfer must not pin memory.
- **notifications** are fire-once events, with no replay to late subscribers.
- **headunit** is a bidirectional relay: the on-device frontend is the single
  source of truth, pushes its self-describing catalog over `/ws/headunit`, and
  the agent caches, fragments and forwards it without interpreting view or
  setting semantics. Catalog notification framing is
  `[2-byte index][2-byte count][JSON fragment]`.

BLE characteristics: `…0005/0006/0007` navigation, `…0008` alerts, `…0009`
head-unit commands, `…000a` head-unit notifications. See
`docs/companion-notifications-navigation.md` and
`docs/companion-headunit-control.md`.

## Deployment notes

`internal/logging` redirects the stdlib `log` to a daily file **and** stderr
(for journald). Default directory `/data/.local/share/rover-mems/logs`,
overridable with `ROVER_LOG_DIR`, 30-day retention.

Serial access needs `sudo usermod -aG dialout $USER` (re-login required).

## Documentation

Protocol reference: https://github.com/james-portman/rover-mems-documentation/tree/master
