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
	if f.Max != 4 || f.Idle != time.Hour || f.MaxAge != 4*time.Hour || f.OwnerGrace != 10*time.Minute {
		t.Fatalf("defaults are not the ones of decision 0002: %+v", f)
	}
	nc.Workers = &NodeWorkers{Max: 2, Idle: "5m", MaxAge: "90m", OwnerGrace: "1m"}
	s, err := nc.WorkerSettings()
	if err != nil || s.Max != 2 || s.Idle != 5*time.Minute || s.MaxAge != 90*time.Minute || s.OwnerGrace != time.Minute {
		t.Fatalf("settings: %+v %v", s, err)
	}
	for _, w := range []NodeWorkers{{Idle: "soon"}, {MaxAge: "-1h"}, {OwnerGrace: "0s"}, {Max: -1}} {
		w := w
		nc.Workers = &w
		if _, err := nc.WorkerSettings(); err == nil || !strings.Contains(err.Error(), "node.json") {
			t.Errorf("%+v: %v", w, err)
		}
	}
}
