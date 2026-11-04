package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The two daemons use a real gateway and local, fake Pi sessions.
func TestDeliveryNodeStepReachesOtherMachine(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	a := startNode(t, g, nodeOpts{cmdline: "pi"})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	repo := newRepo(t, "demo")
	merger := newRepo(t, "merger")
	caller, callerID := helloDelivery(t, a, "caller", repo, "claude-opus-4")
	owner, ownerID := helloDelivery(t, b, "merger", merger, "gpt-6")
	eventually(t, "both sessions reach the gateway", func() bool {
		first, ok1, _ := g.gw.Store().Agent(callerID)
		second, ok2, _ := g.gw.Store().Agent(ownerID)
		return ok1 && ok2 && first.Online && second.Online && first.Repo.Project == "demo" && second.Repo.Project == "merger"
	})
	if err := g.gw.Store().SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	var submitted protocol.ChangeSubmitResult
	if err := json.Unmarshal(caller.mustCall(protocol.OpChangeSubmit, protocol.ChangeSubmitArgs{}), &submitted); err != nil {
		t.Fatal(err)
	}
	id := submitted.Change.Change.ID
	t.Cleanup(func() {
		if t.Failed() {
			v, err := g.gw.Store().GetChange(id)
			messages, mErr := g.gw.Store().Messages(b.d.NodeID(), protocol.QueryMessages{SessionID: ownerID})
			t.Logf("change: %+v, error %v; messages: %+v, error %v; gateway log: %v", v, err, messages, mErr, g.logs.lines())
		}
	})
	var assigned bool
	for _, step := range submitted.Change.Steps {
		if step.Step == protocol.StepAccept && step.Owner == (protocol.StepOwner{Kind: protocol.OwnerNode, Node: "merger"}) {
			assigned = true
		}
	}
	if !assigned {
		t.Fatalf("accept was not assigned: %+v", submitted.Change.Steps)
	}
	var msg protocol.Message
	eventually(t, "the remote step owner receives the task", func() bool {
		select {
		case msg = <-owner.Pushes:
			return msg.From == "agent-net" && strings.Contains(msg.Body, id) && strings.Contains(msg.Body, "accept")
		default:
			return false
		}
	})
	if msg.To != ownerID {
		t.Fatalf("task delivered to %s, want %s", msg.To, ownerID)
	}
}

func TestDeliveryNodeStepWaitsForSession(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	a := startNode(t, g, nodeOpts{cmdline: "pi"})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	repo := newRepo(t, "demo")
	merger := newRepo(t, "merger")
	caller, callerID := helloDelivery(t, a, "caller", repo, "claude-opus-4")
	eventually(t, "caller reaches the gateway", func() bool {
		first, ok, _ := g.gw.Store().Agent(callerID)
		return ok && first.Online && first.Repo.Project == "demo"
	})
	if err := g.gw.Store().AddArchitect("merger", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	var submitted protocol.ChangeSubmitResult
	if err := json.Unmarshal(caller.mustCall(protocol.OpChangeSubmit, protocol.ChangeSubmitArgs{}), &submitted); err != nil {
		t.Fatal(err)
	}
	id := submitted.Change.Change.ID
	if !strings.Contains(submitted.Change.Steps[0].Note, "waiting for merger (offline)") {
		t.Fatalf("offline note: %+v", submitted.Change.Steps[0])
	}
	owner, ownerID := helloDelivery(t, b, "merger", merger, "gpt-6")
	eventually(t, "the session coming online receives its queued task", func() bool {
		select {
		case msg := <-owner.Pushes:
			return msg.To == ownerID && msg.From == "agent-net" && strings.Contains(msg.Body, id)
		default:
			return false
		}
	})
}
