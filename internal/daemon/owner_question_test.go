package daemon

import (
	"encoding/json"
	"reflect"
	"strings"
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

// A turn that ends on a marked owner question gets a question ask at once and
// ends there. A second stop on the same question makes no second ask.
func TestStopOnOwnerQuestionRoutesItAndEndsTheTurn(t *testing.T) {
	g, cli, sid := stopRig(t)
	last := "Built and tested.\nOwner question: ship v1.2 today?"
	stop(t, cli, last)
	if asks := questionAsks(t, g.gw.Store(), sid); len(asks) != 1 || asks[0].Question != "ship v1.2 today?" {
		t.Fatalf("asks after the stop: %+v", asks)
	}
	stop(t, cli, last)
	if asks := questionAsks(t, g.gw.Store(), sid); len(asks) != 1 {
		t.Fatalf("asks after the second stop: %+v", asks)
	}
}

// An idle agent whose turn ends with no question gets no ask.
func TestStopWithoutQuestionPasses(t *testing.T) {
	g, cli, sid := stopRig(t)
	stop(t, cli, "Done. All tests pass. Should I also tidy the docs?")
	if asks := questionAsks(t, g.gw.Store(), sid); len(asks) != 0 {
		t.Fatalf("plain turn end made asks: %+v", asks)
	}
}

// The owner typed in the session a moment ago. The question is in his chat,
// and it follows the route too.
func TestStopWithTheOwnerTypingStillRoutesTheQuestion(t *testing.T) {
	g, cli, sid := stopRig(t)
	askPolicy(t, cli, protocol.PolicyRequest{Harness: "claude", NativeID: "c1", Prompt: "build the release"})
	stop(t, cli, "Built.\nOwner question: ship v1.2 today?")
	if asks := questionAsks(t, g.gw.Store(), sid); len(asks) != 1 {
		t.Fatalf("asks with the owner typing: %+v", asks)
	}
}

// Pi has no Stop hook: an agent_owner_ask it left open is caught at the start
// of its next turn and gets its ask, with no line added to the turn.
func TestPiOpenOwnerAskIsRoutedAtTheNextTurn(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	pi, sid := helloPi(t, n, "p1", "/w/one", true)
	pi.mustCall(protocol.MethodWaiting, protocol.WaitingRequest{Explicit: true, Kind: protocol.WaitingQuestion, Text: "Which region for the new box?"})
	if r := askPolicy(t, pi, protocol.PolicyRequest{}); strings.Contains(r.Charter, "Send this question to the chief") {
		t.Fatalf("next turn: charter %q, want no line about the question", r.Charter)
	}
	if asks := questionAsks(t, g.gw.Store(), sid); len(asks) != 1 {
		t.Fatalf("asks for the open question: %+v", asks)
	}
}

// A Pi session that takes its ask back does not stay waiting on the
// withdrawal text, and the text makes no new ask.
func TestPiWithdrawalClearsTheWaitAndMakesNoAsk(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	pi, sid := helloPi(t, n, "p1", "/w/one", true)
	first := pi.mustCall("ask_question", map[string]any{"text": "Which region for the new box?", "options": []string{"eu", "us"}, "recommendation": "eu"})
	var created struct {
		Item protocol.InboxItem `json:"item"`
	}
	if err := json.Unmarshal(first, &created); err != nil || created.Item.ID == "" {
		t.Fatalf("first ask: %s, %v", first, err)
	}
	eventually(t, "the wait is stored", func() bool {
		a, ok, _ := g.gw.Store().Agent(sid)
		return ok && a.Waiting != nil && a.Waiting.Text == "Which region for the new box?"
	})

	res := pi.mustCall("ask_question", map[string]any{"text": "Withdraw " + created.Item.ID})
	var out struct {
		Withdrawn bool `json:"withdrawn"`
	}
	if json.Unmarshal(res, &out) != nil || !out.Withdrawn {
		t.Fatalf("withdrawal answer: %s", res)
	}
	eventually(t, "the wait is cleared", func() bool {
		a, ok, _ := g.gw.Store().Agent(sid)
		return ok && a.Waiting == nil
	})
	if asks := questionAsks(t, g.gw.Store(), sid); len(asks) != 0 {
		t.Fatalf("the withdrawal left an ask open: %+v", asks)
	}
	var made int
	items, err := g.gw.Store().ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if strings.HasPrefix(item.Text, "Withdraw ") {
			made++
		}
	}
	if made != 0 {
		t.Fatalf("the withdrawal text made %d new asks", made)
	}
}
