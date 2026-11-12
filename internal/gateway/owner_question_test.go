package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// No agent asks the owner in its own chat (decision 0150, plan step c2).

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

func turnEnd(t *testing.T, g *Gateway, node, session, text string) (redirect bool, item string) {
	t.Helper()
	res := nodeCall(t, g, node, "ask.turn_end", map[string]any{"session_id": session, "text": text})
	redirect, _ = res["redirect"].(bool)
	if it, ok := res["item"].(map[string]any); ok {
		item, _ = it["id"].(string)
	}
	return redirect, item
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

// A turn that ends on a question while the owner is away becomes a
// chief-bound ask at once. The owner only viewing the session does not count
// as working in it.
func TestTurnEndQuestionGoesToTheChiefWhenTheOwnerIsAway(t *testing.T) {
	s, _, g, _, asker, node := questionRig(t)
	if err := s.RecordOwnerUse(asker); err != nil {
		t.Fatal(err)
	}
	redirect, id := turnEnd(t, g, node, asker, "Ship the release today?")
	if !redirect {
		t.Fatal("a question with the owner away was not redirected")
	}
	wantChiefBound(t, s, id)
}

// The owner typed in the session 5 minutes ago: he is working in it, so a
// question to him there is normal. Nothing is redirected and no ask is made,
// until he has not typed for 30 minutes.
func TestTurnEndQuestionStaysWhenTheOwnerTypedLately(t *testing.T) {
	s, c, g, _, asker, node := questionRig(t)
	nodeCall(t, g, node, "session.owner_use", map[string]any{"session_id": asker, "prompt": true, "text": "go on"})
	c.t = c.t.Add(5 * time.Minute)
	if redirect, _ := turnEnd(t, g, node, asker, "Ship the release today?"); redirect {
		t.Fatal("redirected while the owner works in the session")
	}
	if n := openQuestions(t, s, asker); n != 0 {
		t.Fatalf("%d asks while the owner works in the session", n)
	}
	c.t = c.t.Add(ownerPresentWindow)
	if redirect, _ := turnEnd(t, g, node, asker, "Ship the release today?"); !redirect {
		t.Fatal("not redirected 35 minutes after the owner typed")
	}
}

// The chief and the CTO are the front door: they talk to the owner in their
// own chats and are never redirected.
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
		if redirect, _ := turnEnd(t, g, x.node, x.session, "Which plan do you want?"); redirect {
			t.Fatalf("front door session %s was redirected", x.session)
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

// A turn that ends with an agent_owner_ask open is redirected without a second
// ask. The explicit wait of Pi or OpenCode has no ask yet, so it gets one.
func TestTurnEndOpenAskMakesOneAsk(t *testing.T) {
	s, _, g, _, asker, node := questionRig(t)
	ask := map[string]any{"session_id": asker, "text": "Which region for the new box?", "asked": true}
	if res := nodeCall(t, g, node, "ask.turn_end", ask); res["redirect"] != true {
		t.Fatalf("open ask with the owner away: %+v", res)
	}
	if n := openQuestions(t, s, asker); n != 1 {
		t.Fatalf("%d asks for an explicit wait without one, want 1", n)
	}
	if res := nodeCall(t, g, node, "ask.turn_end", ask); res["redirect"] != true {
		t.Fatalf("second stop: %+v", res)
	}
	if n := openQuestions(t, s, asker); n != 1 {
		t.Fatalf("%d asks after the second stop, want still 1", n)
	}
}
