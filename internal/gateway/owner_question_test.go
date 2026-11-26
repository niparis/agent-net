package gateway

import (
	"context"
	"encoding/json"
	"testing"
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
