package gateway

import (
	"testing"
	"time"
)

// A daemon needed about 930 ms to fingerprint 23 destinations. The batch call
// must wait longer than that, or the gateway probes every destination again.
func TestToolProbeBatchWaitGrowsWithTheProbes(t *testing.T) {
	if got := toolProbeBatchWait(23); got < 1200*time.Millisecond {
		t.Fatalf("23 probes wait %v", got)
	}
	if toolProbeBatchWait(40) <= toolProbeBatchWait(23) {
		t.Fatal("more probes must wait longer")
	}
	if got := toolProbeBatchWait(0); got != 750*time.Millisecond {
		t.Fatalf("no probes wait %v", got)
	}
	// A legacy daemon stays silent and the wait ends the call, so the wait
	// must stay short for a plan of the usual size.
	if got := toolProbeBatchWait(40); got > 2500*time.Millisecond {
		t.Fatalf("40 probes wait %v, a legacy daemon would wait that long first", got)
	}
}
