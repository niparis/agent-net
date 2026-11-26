package daemon

import (
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

// An owner question shows in the agent's own chat AND follows the route to the
// chief and the Desk (decision 0150, plan step c2). The owner's words, typed to
// the agent-net lead on 2026-11-12: "the question should be in the chat AND
// follow the route".

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

// stop runs the Stop hook's daemon call. The turn always ends as it is: the
// answer has nothing that makes the session go on.
func stop(t *testing.T, cli *localClient, last string) {
	t.Helper()
	if r := askPolicy(t, cli, protocol.PolicyRequest{Harness: "claude", NativeID: "c1", Event: "Stop", LastMessage: last}); !reflect.DeepEqual(r, protocol.PolicyResult{}) {
		t.Fatalf("stop answer %+v, want none: the question stays the last thing in the chat", r)
	}
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

// An idle agent whose turn ends with no question gets no ask.
func TestStopWithoutQuestionPasses(t *testing.T) {
	g, cli, sid := stopRig(t)
	stop(t, cli, "Done. All tests pass. Should I also tidy the docs?")
	if asks := questionAsks(t, g.gw.Store(), sid); len(asks) != 0 {
		t.Fatalf("plain turn end made asks: %+v", asks)
	}
}
