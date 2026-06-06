# Architecture TODO

Findings from the architecture review, ranked major → minor. Severity: 🔴 major · 🟠 moderate · 🟢 minor.

**Status: all items done** (one commit each, see refs).

## 🔴 Major

- [x] **2J data/faults never reach the API.** `mems2j` wrote its own `m.data`/`m.faults` but the web layer only reads `ecu.State`. Now writes the shared `*ecu.State` like the other handlers. — `b23a0a2`
- [x] **`ECU` interface is a half-abstraction.** Removed the unused `GetFaults()`/`SendCommand()` (and 2J's `GetData`/`IsConnected`) from the interface and handlers; data/commands flow through `ecu.State`. — `adabac7`
- [x] **Shutdown is duplicated and non-graceful.** Centralized on a single `signal.NotifyContext` in main, passed to `web.Run` and the event loop; `srv.Shutdown` now runs. — `14ba0b4`

## 🟠 Moderate

- [x] **No `context.Context` in `Connect`/`ReadData`.** Threaded `ctx` through the interface and all handlers; loops check `ctx.Done()`. — `546c7e9`
- [x] **Mutable package-level state in `mems1x`.** `gotKlineEcho`/`lastKlineByte` moved onto the `MEMS1x` struct. — `f287f6d`
- [x] **Inconsistent logging + swallowed errors.** `mems2j`/`mems3`/`rc5` route through the `State` logger; `sp.Write` errors captured; `Connect` open/SetMode errors wrapped with `%w`. — `2bff50d`

## 🟢 Minor

- [x] **`State` leaks its mutex.** Added `Snapshot()`, `ConsumeAlertError()`, and setters; migrated `server.go` off manual locking; the GET drain is now explicit. (Handlers keep `Lock/Unlock` for batched writes.) — `d4bc7c5`
- [x] **WebSocket non-`"."` branch is a stub.** Removed the fabricated `{"command":"worked"}` reply; unknown messages are ignored. — `0d7f1d8`
- [x] **No tests, no linter config.** Added table-driven `parseData80`/`parseData7D` tests (regression guards on the scaling fixes) and `.golangci.yml`. — `0f56b1e`
- [x] **`main.go` `LogDebug` with `%s` directives.** Switched to `LogDebugf`; `go vet ./...` is clean. — `f447214`

---

# Code Quality Review (2026-06-06)

Findings from the code-quality pass, ranked. Severity: 🔴 major · 🟠 moderate · 🟢 minor.

## 🔴 Major

- [ ] **Goroutine leak on every reconnect.** `serial.Reader.Start` (`internal/serial/readwrite.go`) spins a `for { sp.Read(...) }` goroutine with no stop signal, and `MEMS2J.Close()` never stops it. The main loop reconnects every 1s, so each failed 2J connect leaks a busy-looping goroutine. Add a stop mechanism (done channel / context) and call it from `Close()`.
- [ ] **Unbounded slice indexing can crash the agent.** `parseData80`/`parseData7D` (`internal/ecu/mems1x/loop.go`) read fixed offsets (`data[14]`, `data[0xE]`, …) but only guard trailing fields by `packetSize`. A short/corrupt frame → index-out-of-range panic in the ECU goroutine, which is **not** behind `gin.Recovery()`, so it kills the process. Guard base-field access (length check / recover in the loop).

## 🟠 Moderate

- [ ] **`panic(err)` in library code.** `GetPortsList` (`internal/serial/ports_linux.go:17`) panics if `/dev/` can't be opened instead of returning the error it already declares. Return the error.
- [ ] **`log.Fatal` inside the web server.** `web/server.go:113,123` call `log.Fatal` on bind/shutdown failure, killing the whole agent (incl. the active ECU connection) from a goroutine. Surface the error instead of exiting the process.
- [ ] **Ignored serial read errors.** 8 sites use `n, _ := sp.Read(...)` (mems1x, mems19, mems3, rc5, readwrite). A hard error (unplugged USB) returns `n=0, err!=nil` and the code spins to timeout. Distinguish a hard error from an empty read.
- [ ] **gofmt failures.** `ports_linux.go`, `ports_windows.go`, `loop_test.go` have mixed tabs/spaces. Run `gofmt -w .`.

## 🟢 Minor

- [ ] **`fmt.Println` bypassing the logger.** `loop.go:88,196,211` (and a few others) write directly to stdout, ignoring `DebugMode` and never reaching `LogLines`/the web UI. Route through `state.LogDebug`.
- [ ] **`interface{}` over `any`.** `State.LogDebug`/`LogDebugf` (`ecu.go:50,65`) use `interface{}`; repo targets go 1.23 and `any` is the convention (mems2j already uses it).
