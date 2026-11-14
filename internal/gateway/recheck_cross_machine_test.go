package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// twoMachineRecheck returns a gateway with an owner session on one machine and
// a queued start tracked on another. The start was handed to the second
// machine, so that machine sends the recheck when it leaves the queue.
func twoMachineRecheck(t *testing.T) (*Gateway, *nodeConn, *nodeConn, string) {
	t.Helper()
	g, nodeA, ownerA, _, c := setupFloorGateway(t)
	nodeB, _, err := g.store.CreateNode("research", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.store.SetNodeHello(nodeB, "research", "1"); err != nil {
		t.Fatal(err)
	}
	launch := true
	if err := g.store.SetNodeLaunch(nodeB, &launch); err != nil {
		t.Fatal(err)
	}
	harnesses := []protocol.HarnessInfo{
		{Name: "opencode", Models: []string{"deepseek/deepseek-v4.1-flash"}},
	}
	if _, err := g.store.SetNodeHarnesses(nodeB, harnesses); err != nil {
		t.Fatal(err)
	}
	if _, err := g.store.AddProjects(nodeB, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "alpha", Path: "/work/alpha"}}}); err != nil {
		t.Fatal(err)
	}
	ncB := &nodeConn{id: nodeB}
	g.conns[nodeB] = ncB
	t.Cleanup(func() { delete(g.conns, nodeB) })
	quotas := &protocol.Quotas{
		GeneratedAt: c.now().Unix(),
		Providers: []protocol.QuotaProvider{
			{Provider: "deepseek", Windows: []protocol.QuotaWindow{{ID: "5h", Kind: "five_hour", PercentUsed: 20, ResetsAt: c.now().Add(2 * 3600).Unix()}}},
		},
	}
	g.usage.set(nodeB, c.now(), protocol.UsageReport{Quotas: quotas})
	return g, g.conns[nodeA], ncB, ownerA
}

// A queued start whose owner session is on machine A passes the recheck when
// it leaves the queue on machine B. The gateway checks the tracked start it
// sent to B, not that the owner session is local there (decision 0162).
func TestQueuedRecheckAllowsAnOwnerOnAnotherMachine(t *testing.T) {
	g, _, ncB, ownerA := twoMachineRecheck(t)
	if err := g.store.TrackWorkerStart("q_cross", ncB.id, ownerA, "alpha", "opencode", ""); err != nil {
		t.Fatal(err)
	}
	request := protocol.WorkerRecheck{Ref: "q_cross", Owner: ownerA, Project: "alpha", Difficulty: "hard",
		Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash:high"}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := g.handleWorker(context.Background(), ncB, "worker.recheck", raw)
	if err != nil {
		t.Fatalf("worker.recheck: %v", err)
	}
	got, ok := answer.(protocol.WorkerRecheckAnswer)
	if !ok {
		t.Fatalf("answer type %T", answer)
	}
	if got.Refused {
		t.Fatalf("the recheck refused a start the gateway queued on this machine: %s", got.Reason)
	}
	if got.Harness != "opencode" || !strings.HasPrefix(got.Model, "deepseek/deepseek-v4.1-flash") {
		t.Fatalf("recheck did not keep the queued model: %+v", got)
	}
}

// A machine cannot recheck a start the gateway did not send it: the tracked
// start names the machine and the owner, and both must match.
func TestQueuedRecheckRefusesAStartTheGatewayDidNotSend(t *testing.T) {
	g, ncA, ncB, ownerA := twoMachineRecheck(t)
	ownerB, err := g.store.UpsertSession(ncB.id, protocol.SessionInfo{Harness: "pi", NativeID: "caller-b"})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.store.TrackWorkerStart("q_elsewhere", ncA.id, ownerA, "alpha", "opencode", ""); err != nil {
		t.Fatal(err)
	}
	for _, request := range []protocol.WorkerRecheck{
		// The start belongs to another machine.
		{Ref: "q_elsewhere", Owner: ownerA, Project: "alpha", Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash:high"},
		// The reference was never tracked at all.
		{Ref: "q_invented", Owner: ownerB, Project: "alpha", Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash:high"},
	} {
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		answer, err := g.handleWorker(context.Background(), ncB, "worker.recheck", raw)
		if err != nil {
			t.Fatalf("%s: worker.recheck: %v", request.Ref, err)
		}
		got, ok := answer.(protocol.WorkerRecheckAnswer)
		if !ok {
			t.Fatalf("%s: answer type %T", request.Ref, answer)
		}
		if !got.Refused || !strings.Contains(got.Reason, request.Ref) {
			t.Fatalf("%s: the recheck did not refuse an unowned start: %+v", request.Ref, got)
		}
	}
}
