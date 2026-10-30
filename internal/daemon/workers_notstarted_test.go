package daemon

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// When the person at the machine's own command line queued the start, there
// is no one to send a "not started" message to. The gateway records an event
// so the TUI feed shows it.
func TestNotStartedByTheLocalOwnerIsAnEvent(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	set := queueSet
	set.QueueTimeout = 200 * 1e6 // 200 ms
	w := startWorkerNode(t, g, set, withLoad(fl))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	r, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	if err != nil || r.Status != "queued" {
		t.Fatalf("%+v %v", r, err)
	}
	eventually(t, "a worker.not_started event", func() bool {
		evs, _ := g.gw.Store().Events("worker.not_started")
		return len(evs) == 1 && strings.Contains(evs[0], "project=shop") && strings.Contains(evs[0], "waited")
	})
}
