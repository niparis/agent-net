package diagnostics

import (
	"testing"
	"time"
)

func TestSlowHandler(t *testing.T) {
	var durations []int64
	handler := func() {
		defer Slow(time.Now(), func(ms int64) { durations = append(durations, ms) })
		time.Sleep(250 * time.Millisecond)
	}
	handler()
	if len(durations) != 1 || durations[0] < 250 {
		t.Fatalf("slow lines: %v", durations)
	}
}
func TestSlowFast(t *testing.T) {
	Slow(time.Now(), func(int64) { t.Fatal("fast call logged") })
}
