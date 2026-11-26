package daemon

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// An automatic worker whose model the machine cannot run ("Model unavailable"
// from OpenCode) does not end the start: its notice says the start moves on,
// and a worker on the next model starts with the same task.
func TestWorkerStartMovesToTheNextModelWhenTheMachineCannotRunIt(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	w.register(t, newRepo(t, "shop"))
	score := 42.3
	for _, m := range []string{"fake/m1", "fake/m2"} {
		if err := g.gw.Store().AddWorkerProfile(protocol.WorkerProfile{Name: m, Harness: "opencode", Model: m, AddedBy: w.ownerID()}); err != nil {
			t.Fatal(err)
		}
		w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: m, Score: &score})
	}
	w.cli.mustCall(protocol.OpWorkerProvidersSet, protocol.WorkerProviders{Providers: []string{"fake"}})

	once := filepath.Join(t.TempDir(), "unavailable-once")
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "auto", Task: "FAKE:unavailable-first=" + once + " do it"})
	first := res.Model
	w.workerStopped(t, g, protocol.StopModelFails)

	var ended, started string
	eventually(t, "the failure notice and the next worker's start notice", func() bool {
		for _, n := range w.messagesOf(t, w.ownerID()) {
			switch {
			case n.From == res.SessionID && strings.Contains(n.Body, "Model unavailable: "+first):
				ended = n.Body
			case n.From != res.SessionID && strings.Contains(n.Body, "\nstatus: started\n"):
				started = n.Body
			}
		}
		return ended != "" && started != ""
	})
	if !strings.Contains(ended, movedToNextModelNote) {
		t.Fatalf("the failure notice does not say the start moves on:\n%s", ended)
	}
	other := "fake/m1"
	if first == other {
		other = "fake/m2"
	}
	if !strings.Contains(started, other) {
		t.Fatalf("the next worker does not run %s:\n%s", other, started)
	}
}
