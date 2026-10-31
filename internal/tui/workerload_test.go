package tui

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The machine row says how many workers run and how much memory they use.
func TestMachineRowShowsRunningWorkersAndMemory(t *testing.T) {
	u := fakeUsage()
	u.Local.Workers = &protocol.WorkerLoad{Running: 2, Queued: 1, Room: protocol.RoomCPU}
	m := modelWith(fakeSnapshot(), u, 200, 40)
	if v := m.View(); !strings.Contains(v, "2 workers · ?") {
		t.Fatalf("the machine row lacks the worker count:\n%s", v)
	}
	u = fakeUsage()
	u.Local.Workers = &protocol.WorkerLoad{Room: protocol.RoomOK}
	m = modelWith(fakeSnapshot(), u, 200, 40)
	if v := m.View(); strings.Contains(v, "0 workers ·") {
		t.Fatalf("an idle machine shows a worker count:\n%s", v)
	}
}
