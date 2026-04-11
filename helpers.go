package main

import (
	"fmt"
	"time"
)

func slicesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

func xor_all_bytes(bytes []byte) byte {
	output := byte(0)
	for i := 0; i < len(bytes); i++ {
		output = output ^ bytes[i]
	}
	return output
}

func sleepUntil(start time.Time, plus int) {
	target := start.Add(time.Duration(plus) * time.Millisecond)
	sleepMs := time.Until(target).Milliseconds()
	if sleepMs < 0 {
		return
	}
	time.Sleep(time.Duration(sleepMs) * time.Millisecond)
}

func timestampMs() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

const maxLogLines = 500

func appendLogLine(line string) {
	globalDataOutputLock.Lock()
	globalLogLines = append(globalLogLines, line)
	if len(globalLogLines) > maxLogLines {
		globalLogLines = globalLogLines[len(globalLogLines)-maxLogLines:]
	}
	globalDataOutputLock.Unlock()
}

func logDebug(a ...interface{}) {
	line := fmt.Sprint(a...)
	appendLogLine(line)
	if globalDebugMode {
		fmt.Println(line)
	}
}

func logDebugf(format string, a ...interface{}) {
	line := fmt.Sprintf(format, a...)
	appendLogLine(line)
	if globalDebugMode {
		fmt.Println(line)
	}
}
