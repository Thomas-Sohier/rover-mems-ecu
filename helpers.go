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

func logDebug(a ...interface{}) {
	if globalDebugMode {
		fmt.Println(a...)
	}
}

func logDebugf(format string, a ...interface{}) {
	if globalDebugMode {
		fmt.Printf(format+"\n", a...)
	}
}
