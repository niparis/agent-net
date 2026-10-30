package daemon

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A worker's task reaches the agent list on every machine, so a status view can
// name the worker after it instead of its worker ID (decision 0017, part A).
func TestWorkerTaskReachesTheAgentList(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	w.register(t, newRepo(t, "shop"))
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "deploy the routes\nthen test them"})

	eventually(t, "the worker's task in the agent list", func() bool {
		for _, a := range w.agents(t, false) {
			if a.SessionID == res.SessionID {
				return a.Worker != nil && a.Worker.Task == "deploy the routes"
			}
		}
		return false
	})

	// A later request from the owner updates the task, without changing the
	// value another response may still be writing.
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "and run the smoke test\nonce"}), &struct{}{})
	eventually(t, "the worker's later task in the agent list", func() bool {
		for _, a := range w.agents(t, false) {
			if a.SessionID == res.SessionID {
				return a.Worker != nil && a.Worker.Task == "and run the smoke test"
			}
		}
		return false
	})
}
