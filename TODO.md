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
