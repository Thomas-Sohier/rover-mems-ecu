# Refactoring TODO

## Error 1: Package declaration conflicts ✅ FIXED

Files in `internal/` still declare `package main` instead of their correct package.

| File | Current | Should Be |
|------|---------|-----------|
| `internal/ecu/auth.go` | `package main` | `package ecu` |
| `internal/web/server.go` | `package main` | `package web` |
| `internal/serial/readwrite.go` | `package main` | `package serial` |
| `internal/serial/ports_linux.go` | `package main` | `package serial` |
| `internal/serial/ports_windows.go` | `package main` | `package serial` |
| `internal/ecu/mems2j/mems2j.go` | `package main` | `package mems2j` |
| `internal/ecu/mems2j/faults.go` | `package main` | `package mems2j` |
| `internal/ecu/mems2j/parse.go` | `package main` | `package mems2j` |
| `internal/ecu/fake/fake.go` | `package main` | `package fake` |
| `internal/ecu/mems3/mems3.go` | `package main` | `package mems3` |
| `internal/ecu/mems1x/init.go` | `package main` | `package mems1x` |
| `internal/ecu/mems1x/loop.go` | `package main` | `package mems1x` |
| `internal/ecu/rc5/rc5.go` | `package main` | `package rc5` |
| `internal/ecu/mems19/mems19.go` | `package main` | `package mems19` |

---

## Error 2: Embed path incorrect ✅ FIXED

**File:** `internal/web/server.go:21`

**Fix:** Copied `ui/dashboard.html` to `internal/web/dashboard.html` (Go embed can't use `..` paths)

---

## Error 3: Global variable references

After fixing packages, each file will have undefined references to globals:
- `globalDataOutput`
- `globalDataOutputLock`
- `globalConnected`
- `globalFaults`
- `globalAlert`
- `globalError`
- `globalUserCommand`
- `globalDebugMode`
- `globalEcuType`
- `globalSelectedSerialPort`
- `globalSerialPorts`
- `globalLogLines`
- `globalAgentVersion`

**Fix:** Replace global variable usage with `*ecu.State` dependency injection - pass State to constructors and methods.

---

## Error 4: Cross-package function calls

Functions in ECU packages call each other:
- `mems19` calls `ecu1xLoop` from `mems1x`
- `mems19` calls `send5BaudWakeup`, `handleWakeUpHandshake` etc.
- Various packages use shared serial helpers

**Fix:** Export shared functions (capitalize names) and update imports across packages.

---

## Error 5: Serial channel references

Files reference `serialReadChannel` which is a global channel for async serial reads.

**Fix:** Move channel into a `SerialPort` struct in `internal/serial/` and pass it via dependency injection.
