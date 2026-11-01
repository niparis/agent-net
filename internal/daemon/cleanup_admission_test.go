package daemon

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCleanupNoDiskAdmission(t *testing.T) {
	t.Setenv("AGENT_NET_MIN_FREE_GB", "999999")
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, queueSet, func(c *Config) {
		c.LoadProbe = func() MachineLoad { return roomyLoad() }

	})
	repo := newRepo(t, "diskfree")
	w.register(t, repo)
	pi, _ := helloPi(t, w.node, "caller", "/work/caller", true)
	got := startFrom(t, pi, protocol.WorkerStartRequest{Project: "diskfree", Harness: "opencode", Task: "test"})
	if got.Status == "queued" {
		t.Fatalf("unexpected disk queue: %+v", got)
	}
}
