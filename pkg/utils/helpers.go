package utils

import (
	"time"
)

func SlicesEqual(a, b []byte) bool {
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

func XorAllBytes(bytes []byte) byte {
	output := byte(0)
	for i := 0; i < len(bytes); i++ {
		output = output ^ bytes[i]
	}
	return output
}

func SleepUntil(start time.Time, plusMs int) {
	target := start.Add(time.Duration(plusMs) * time.Millisecond)
	sleepMs := time.Until(target).Milliseconds()
	if sleepMs < 0 {
		return
	}
	time.Sleep(time.Duration(sleepMs) * time.Millisecond)
}

func TimestampMs() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}
