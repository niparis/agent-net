package tui

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The machine heading says how many workers run and wait, and whether a new
// one would start. A machine with no workers and room says nothing.
func TestMachineHeadingShowsWorkersAndRoom(t *testing.T) {
	u := fakeUsage()
	u.Local.Workers = &protocol.WorkerLoad{Running: 2, Queued: 1, Room: protocol.RoomCPU}
	m := modelWith(fakeSnapshot(), u, 200, 40)
	if v := m.View(); !strings.Contains(v, "workers 2 running, 1 queued · room: cpu") {
		t.Fatalf("the machine heading lacks the worker load:\n%s", v)
	}
	u = fakeUsage()
	u.Local.Workers = &protocol.WorkerLoad{Room: protocol.RoomOK}
	m = modelWith(fakeSnapshot(), u, 200, 40)
	if v := m.View(); strings.Contains(v, "running,") {
		t.Fatalf("an idle machine with room shows a worker line:\n%s", v)
	}
}
