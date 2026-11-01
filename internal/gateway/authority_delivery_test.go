package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/guidance"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestDeliveryUsesReceiverMergeAuthority(t *testing.T) {
	s, _ := newTestStore(t)
	ids, node := graphSessions(t, s, "sender", "receiver")
	if err := s.SetCharter("sender", "maturity", "early", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("receiver", "maturity", "mature", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEdge(protocol.Edge{From: "sender", To: "receiver", Kind: "depends", Rule: "auto"}, "owner"); err != nil {
		t.Fatal(err)
	}
	connections := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		connections <- ws
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	ws := <-connections
	defer ws.CloseNow()
	g := &Gateway{store: s, conns: map[string]*nodeConn{node: {id: node, ws: ws}}}
	for _, authority := range []string{protocol.MergeFull, protocol.MergeDelegated, protocol.MergeAsk} {
		t.Run(authority, func(t *testing.T) {
			if err := s.SetNodeSetting("receiver", protocol.SettingMergeAuthority, authority, "owner"); err != nil {
				t.Fatal(err)
			}
			msg, _, err := send(s, ids["sender"], ids["receiver"], authority, "")
			if err != nil {
				t.Fatal(err)
			}
			msg.OwnerInstruction = "ask receiver to merge example/repo#12"
			msg.RecipientMergeAuthority = "forged sender value"
			g.sendDeliver(ctx, node, msg)
			_, raw, err := client.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var frame protocol.MsgDeliver
			if err := json.Unmarshal(raw, &frame); err != nil {
				t.Fatal(err)
			}
			m := frame.Message
			if m.RecipientMergeAuthority != authority || !m.RecipientTrust {
				t.Fatalf("wrong delivery authority: %+v; frame %s", m, raw)
			}
			graph, err := s.Graph()
			if err != nil {
				t.Fatal(err)
			}
			text := strings.Join([]string{
				protocol.AuthorityNote(m.RecipientTrust, m.FromWorker, m.RecipientMergeAuthority),
				m.RecipientRelation, protocol.NodeContext(graph, "receiver"),
				protocol.OwnerInstructionNote(m.OwnerInstruction, m.RecipientMergeAuthority), guidance.Block(guidance.Other),
			}, "\n")
			rule := (protocol.MaturityDefaults{MergeAuthority: authority}).MergeText()
			if strings.Count(text, rule) != 3 {
				t.Fatalf("receiver must see one rule in all notes: %s", text)
			}
			for _, other := range []string{protocol.MergeFullText, protocol.MergeDelegatedText, protocol.MergeAskText} {
				if other != rule && strings.Contains(text, other) {
					t.Fatalf("contradicting merge rule %q: %s", other, text)
				}
			}
		})
	}
	// Clearing an override restores maturity, rather than the sender's full authority.
	if err := s.SetNodeSetting("receiver", protocol.SettingMergeAuthority, "", "owner"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.MergeAuthorityForSession(ids["receiver"]); err != nil || got != protocol.MergeDelegated {
		t.Fatalf("maturity default %q: %v", got, err)
	}
}
