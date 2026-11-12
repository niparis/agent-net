package daemon

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

// No agent asks the owner in its own chat (decision 0150, plan step c2).

// stopRig is a node with one Claude session that the Stop hook names.
func stopRig(t *testing.T) (*testGateway, *localClient, string) {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "claude"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "/usr/bin/pi"})
	fa.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "c1", Cwd: "/code", Status: "idle"})
	var sid string
	eventually(t, "Claude registered", func() bool {
		for _, s := range n.d.Sessions() {
			if s.NativeID == "c1" {
				sid = s.SessionID
			}
		}
		return sid != ""
	})
	return g, cliClient(t, n), sid
}

func stop(t *testing.T, cli *localClient, last string) string {
	t.Helper()
	return askPolicy(t, cli, protocol.PolicyRequest{Harness: "claude", NativeID: "c1", Event: "Stop", LastMessage: last}).Continue
}

func questionAsks(t *testing.T, st *gateway.Store, sid string) []protocol.Ask {
	t.Helper()
	asks, err := st.ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	var out []protocol.Ask
	for _, a := range asks {
		if a.Kind == protocol.AskKindQuestion && a.FromSession == sid {
			out = append(out, a)
		}
	}
	return out
}

// A turn that ends on a marked owner question, with the owner away, gets a
// question ask at once and the Stop hook's continue reason exactly once. A
// second stop on the same question passes.
func TestStopOnOwnerQuestionContinuesOnce(t *testing.T) {
	g, cli, sid := stopRig(t)
	last := "Built and tested.\nOwner question: ship v1.2 today?"
	if got := stop(t, cli, last); got != protocol.OwnerQuestionRedirect {
		t.Fatalf("first stop: continue %q, want %q", got, protocol.OwnerQuestionRedirect)
	}
	if asks := questionAsks(t, g.gw.Store(), sid); len(asks) != 1 || asks[0].Question != "ship v1.2 today?" {
		t.Fatalf("asks after the stop: %+v", asks)
	}
	if got := stop(t, cli, last); got != "" {
		t.Fatalf("second stop on the same question: continue %q, want none", got)
	}
	if asks := questionAsks(t, g.gw.Store(), sid); len(asks) != 1 {
		t.Fatalf("asks after the second stop: %+v", asks)
	}
}

// An idle agent whose turn ends with no question gets no ask and no continue.
func TestStopWithoutQuestionPasses(t *testing.T) {
	g, cli, sid := stopRig(t)
	if got := stop(t, cli, "Done. All tests pass. Should I also tidy the docs?"); got != "" {
		t.Fatalf("plain turn end: continue %q", got)
	}
	if asks := questionAsks(t, g.gw.Store(), sid); len(asks) != 0 {
		t.Fatalf("plain turn end made asks: %+v", asks)
	}
}

// The owner typed in the session a moment ago: he is in the loop, so the same
// question makes no ask and the hook does not block.
func TestStopWithTheOwnerTypingPasses(t *testing.T) {
	g, cli, sid := stopRig(t)
	askPolicy(t, cli, protocol.PolicyRequest{Harness: "claude", NativeID: "c1", Prompt: "build the release"})
	if got := stop(t, cli, "Built.\nOwner question: ship v1.2 today?"); got != "" {
		t.Fatalf("owner typing: continue %q", got)
	}
	if asks := questionAsks(t, g.gw.Store(), sid); len(asks) != 0 {
		t.Fatalf("owner typing made asks: %+v", asks)
	}
}

// Pi has no Stop hook: an agent_owner_ask it left open is caught at the start
// of its next turn, with the line in the policy answer, once.
func TestPiOpenOwnerAskGetsTheLineAtTheNextTurn(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	pi, sid := helloPi(t, n, "p1", "/w/one", true)
	pi.mustCall(protocol.MethodWaiting, protocol.WaitingRequest{Explicit: true, Kind: protocol.WaitingQuestion, Text: "Which region for the new box?"})
	if r := askPolicy(t, pi, protocol.PolicyRequest{}); !strings.Contains(r.Charter, protocol.OwnerQuestionRedirect) {
		t.Fatalf("next turn: charter %q, want the line", r.Charter)
	}
	if asks := questionAsks(t, g.gw.Store(), sid); len(asks) != 1 {
		t.Fatalf("asks for the open question: %+v", asks)
	}
	if r := askPolicy(t, pi, protocol.PolicyRequest{}); strings.Contains(r.Charter, protocol.OwnerQuestionRedirect) {
		t.Fatalf("the turn after: charter %q, want no line", r.Charter)
	}
}
