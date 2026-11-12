package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// An owner question shows in the agent's own chat AND follows the route to the
// chief and the Desk (decision 0150, plan step c2). The owner's words, typed to
// the agent-net lead on 2026-11-12: "the question should be in the chat AND
// follow the route".

// questionRig is a chief rig with a gateway to call, and the asker's node.
func questionRig(t *testing.T) (s *Store, c *clock, g *Gateway, chief, asker, askerNode string) {
	t.Helper()
	s, c, chief, asker, _ = chiefRig(t)
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, asker).Scan(&askerNode); err != nil {
		t.Fatal(err)
	}
	return s, c, &Gateway{store: s}, chief, asker, askerNode
}

// nodeCall calls one gateway method as a node and decodes the answer.
func nodeCall(t *testing.T, g *Gateway, node, method string, params any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(params)
	res, err := g.handle(context.Background(), &nodeConn{id: node}, method, raw)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	out := map[string]any{}
	if b, err := json.Marshal(res); err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

// turnEnd reports a turn that ended on a question. A daemon before this
// change continues the turn when the answer has redirect true, so the gateway
// never sends it: the question stays the last thing in the chat.
func turnEnd(t *testing.T, g *Gateway, node, session, text string) (routed bool, item string) {
	t.Helper()
	res := nodeCall(t, g, node, "ask.turn_end", map[string]any{"session_id": session, "text": text})
	if res["redirect"] == true {
		t.Fatalf("ask.turn_end answered redirect true, which makes an older daemon continue the turn: %+v", res)
	}
	routed, _ = res["routed"].(bool)
	if it, ok := res["item"].(map[string]any); ok {
		item, _ = it["id"].(string)
	}
	return routed, item
}

func openQuestions(t *testing.T, s *Store, session string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE session_id=? AND kind='question' AND status='open'`, session).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func wantChiefBound(t *testing.T, s *Store, id string) {
	t.Helper()
	if id == "" {
		t.Fatal("no item")
	}
	if _, owner, state, _, _ := itemRow(t, s, id); owner != protocol.DeskOwnerChief || state != protocol.DeskVerifying {
		t.Fatalf("item %s: owner %q state %q, want chief and verifying", id, owner, state)
	}
}

// An agent's agent_owner_ask goes to the chief, also without options: it is
// never an owner-bound item without options.
func TestAgentOwnerAskGoesToTheChief(t *testing.T) {
	s, _, g, _, asker, node := questionRig(t)
	res := nodeCall(t, g, node, "inbox.create", map[string]any{"session_id": asker, "kind": "question", "text": "Which region for the new box?"})
	item, _ := res["item"].(map[string]any)
	id, _ := item["id"].(string)
	wantChiefBound(t, s, id)
}

// A turn that ends on a question becomes a chief-bound ask at once, and the
// question stays in the chat.
func TestTurnEndQuestionGoesToTheChief(t *testing.T) {
	s, _, g, _, asker, node := questionRig(t)
	routed, id := turnEnd(t, g, node, asker, "Ship the release today?")
	if !routed {
		t.Fatal("the question did not follow the route")
	}
	wantChiefBound(t, s, id)
}

// The owner typed in the session 5 minutes ago. The question is in the chat
// he works in, and it follows the route too. When he answers in the chat, the
// ask closes with his words.
func TestTurnEndQuestionIsRoutedWhenTheOwnerTypedLately(t *testing.T) {
	s, c, g, _, asker, node := questionRig(t)
	nodeCall(t, g, node, "session.owner_use", map[string]any{"session_id": asker, "prompt": true, "text": "go on"})
	c.t = c.t.Add(5 * time.Minute)
	routed, id := turnEnd(t, g, node, asker, "Ship the release today?")
	if !routed {
		t.Fatal("not routed while the owner works in the session")
	}
	wantChiefBound(t, s, id)
}

// The chief and the CTO are the front door: they talk to the owner in their
// own chats, and their questions are not routed to the chief.
func TestFrontDoorSessionsAreNeverRedirected(t *testing.T) {
	s, _, g, chief, _, node := questionRig(t)
	var chiefNode string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, chief).Scan(&chiefNode); err != nil {
		t.Fatal(err)
	}
	cto, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct{ session, node string }{{chief, chiefNode}, {cto, node}} {
		if routed, _ := turnEnd(t, g, x.node, x.session, "Which plan do you want?"); routed {
			t.Fatalf("front door session %s was routed", x.session)
		}
		if n := openQuestions(t, s, x.session); n != 0 {
			t.Fatalf("front door session %s got %d asks", x.session, n)
		}
	}
}

// A question prompt the harness reports, with the owner away, is a
// chief-bound ask at once: the short-wait debounce is only for the owner who
// works in the session.
func TestWaitingQuestionIsAChiefAskAtOnceWhenTheOwnerIsAway(t *testing.T) {
	s, c, _, _, asker, node := questionRig(t)
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "nb", Name: "bob",
		Waiting: &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "Which database?", Blocking: true, Since: c.t.Unix()}}); err != nil {
		t.Fatal(err)
	}
	asks, _ := s.ListAsks()
	w := askByKind(asks, protocol.AskKindWaiting)
	if len(w) != 1 || w[0].AboutID != asker {
		t.Fatalf("waiting asks at once: %+v", asks)
	}
	wantChiefBound(t, s, w[0].ID)
}

// A turn that ends with an agent_owner_ask open is routed without a second
// ask. The explicit wait of Pi or OpenCode has no ask yet, so it gets one.
func TestTurnEndOpenAskMakesOneAsk(t *testing.T) {
	s, _, g, _, asker, node := questionRig(t)
	ask := map[string]any{"session_id": asker, "text": "Which region for the new box?", "asked": true}
	if res := nodeCall(t, g, node, "ask.turn_end", ask); res["routed"] != true {
		t.Fatalf("open ask: %+v", res)
	}
	if n := openQuestions(t, s, asker); n != 1 {
		t.Fatalf("%d asks for an explicit wait without one, want 1", n)
	}
	if res := nodeCall(t, g, node, "ask.turn_end", ask); res["routed"] != true {
		t.Fatalf("second stop: %+v", res)
	}
	if n := openQuestions(t, s, asker); n != 1 {
		t.Fatalf("%d asks after the second stop, want still 1", n)
	}
}
