package daemon

import (
	"strings"
	"testing"
	"time"
)

func TestNodeWorkerSettings(t *testing.T) {
	var nc NodeConfig
	if s, err := nc.WorkerSettings(); err != nil || s != (WorkerSettings{}) {
		t.Fatalf("absent: %+v %v", s, err)
	}
	f := (WorkerSettings{}).filled()
	if f.Max != 12 || f.Idle != time.Hour || f.MaxAge != 4*time.Hour || f.OwnerGrace != 10*time.Minute {
		t.Fatalf("defaults are not the ones of decision 0002: %+v", f)
	}
	nc.Workers = &NodeWorkers{Max: 2, Idle: "5m", MaxAge: "90m", OwnerGrace: "1m"}
	s, err := nc.WorkerSettings()
	if err != nil || s.Max != 2 || s.Idle != 5*time.Minute || s.MaxAge != 90*time.Minute || s.OwnerGrace != time.Minute {
		t.Fatalf("settings: %+v %v", s, err)
	}
	nc.Workers = &NodeWorkers{CPUTarget: 60, MemReserve: "2GB", QueueTimeout: "30m", StartGap: "10s"}
	s, err = nc.WorkerSettings()
	if err != nil || s.CPUTarget != 60 || s.MemReserve != 2<<30 || s.QueueTimeout != 30*time.Minute || s.StartGap != 10*time.Second {
		t.Fatalf("load settings: %+v %v", s, err)
	}
	// "0s" turns the start gap off, like a negative duration.
	nc.Workers = &NodeWorkers{StartGap: "0s"}
	if s, err = nc.WorkerSettings(); err != nil || s.StartGap >= 0 {
		t.Fatalf("start gap off: %+v %v", s, err)
	}
	if f := s.filled(); f.StartGap >= 0 {
		t.Fatalf("filled brought the gap back: %+v", f)
	}
	for _, w := range []NodeWorkers{{Idle: "soon"}, {MaxAge: "-1h"}, {OwnerGrace: "0s"}, {Max: -1}, {CPUTarget: 150}, {CPUTarget: -1},
		{MemReserve: "lots"}, {MemReserve: "-2GB"}, {QueueTimeout: "never"}, {StartGap: "later"}} {
		w := w
		nc.Workers = &w
		if _, err := nc.WorkerSettings(); err == nil || !strings.Contains(err.Error(), "node.json") {
			t.Errorf("%+v: %v", w, err)
		}
	}
}
