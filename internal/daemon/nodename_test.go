package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func nodeList(t *testing.T, lc *localClient) []protocol.NodeView {
	t.Helper()
	var info protocol.NodesInfo
	if err := json.Unmarshal(lc.mustCall(protocol.OpNodes, nil), &info); err != nil {
		t.Fatal(err)
	}
	return info.Nodes
}

func TestNodeRenameIsOwnerOnlyAndEveryMachineSeesTheName(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	a := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	b := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	p1, s1 := helloPi(t, a, "p1", "/w/one", true)
	cliA, cliB := cliClient(t, a), cliClient(t, b)
	idA := a.cfg.NodeID

	// An agent session cannot name a machine, its own or another.
	for _, id := range []string{idA, b.cfg.NodeID} {
		_, err := p1.call(protocol.OpNodeRename, protocol.NodeRenameRequest{NodeID: id, Name: "sneaky"})
		if err == nil || !strings.Contains(err.Error(), "only available to the owner") {
			t.Fatalf("an agent session renamed a machine: %v", err)
		}
	}
	for _, n := range nodeList(t, cliB) {
		if n.NodeID == idA && n.DisplayName != "" {
			t.Fatalf("the name changed anyway: %+v", n)
		}
	}

	// The owner on machine B names machine A. B lists it at once.
	raw := cliB.mustCall(protocol.OpNodeRename, protocol.NodeRenameRequest{NodeID: idA, Name: "laptop-1"})
	var res protocol.NodeRenameResult
	if err := json.Unmarshal(raw, &res); err != nil || res.Hostname != "laptop-1" || res.Reported != a.cfg.Hostname {
		t.Fatalf("result: %+v %v", res, err)
	}
	hostOf := func(lc *localClient, session string) string {
		for _, ag := range trustList(t, lc) {
			if ag.SessionID == session {
				return ag.Hostname
			}
		}
		return ""
	}
	if h := hostOf(cliB, s1); h != "laptop-1" {
		t.Fatalf("B lists the session on %q", h)
	}
	// Machine A itself uses the name where it compares host names.
	eventually(t, "A learns its name", func() bool { return a.d.hostName() == "laptop-1" })
	if h := hostOf(cliA, s1); h != "laptop-1" {
		t.Fatalf("A lists its own session on %q", h)
	}

	// A used name is refused with the reason.
	if _, err := cliA.call(protocol.OpNodeRename, protocol.NodeRenameRequest{NodeID: b.cfg.NodeID, Name: "LAPTOP-1"}); err == nil || !strings.Contains(err.Error(), "already called") {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := cliA.call(protocol.OpNodeRename, protocol.NodeRenameRequest{NodeID: b.cfg.NodeID, Name: "bad name"}); err == nil {
		t.Fatal("a bad name was accepted")
	}

	// A reconnect of A (a new hello) keeps the name.
	a.restart()
	eventually(t, "A knows its name after a restart", func() bool { return a.d.hostName() == "laptop-1" })

	// Clearing goes back to what the machine reports, on both sides.
	cliB.mustCall(protocol.OpNodeRename, protocol.NodeRenameRequest{NodeID: idA, Name: ""})
	eventually(t, "A is back to its own name", func() bool { return a.d.hostName() == a.cfg.Hostname })
	for _, n := range nodeList(t, cliB) {
		if n.NodeID == idA && (n.Hostname != a.cfg.Hostname || n.DisplayName != "") {
			t.Fatalf("after clear: %+v", n)
		}
	}
}
