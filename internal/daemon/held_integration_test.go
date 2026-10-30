package daemon

import (
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestHeldOwnerActionsThroughGateway(t *testing.T) {
	for _, action := range []string{"a", "A", "r", "R"} {
		t.Run(action, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
			sender, _ := helloPi(t, n, "sender", "/w/sender", true)
			receiver, id := helloPi(t, n, "receiver", "/w/receiver", true)
			cli := cliClient(t, n)
			sender.mustCall("announce", map[string]any{"project": "sender", "task": "work"})
			receiver.mustCall("announce", map[string]any{"project": "receiver", "task": "work"})
			cli.mustCall("edge_set", protocol.Edge{From: "*", To: "receiver", Kind: "depends", Rule: "ask"})
			sent := sendResult(t, sender.mustCall("send", map[string]any{"to": id, "body": "work"}))
			var held struct {
				Messages []protocol.Message `json:"messages"`
			}
			if err := json.Unmarshal(cli.mustCall("held_list", nil), &held); err != nil || len(held.Messages) != 1 || held.Messages[0].ID != sent.ID {
				t.Fatalf("held: %+v %v sent=%+v agents=%+v", held, err, sent, trustList(t, cli))
			}
			cli.mustCall("held_answer", protocol.HeldAnswer{MessageID: sent.ID, Action: action})
			if action == "a" || action == "A" {
				got := receiver.nextPush()
				if got.ID != sent.ID || !got.OwnerApproved {
					t.Fatalf("approval: %+v", got)
				}
			} else {
				got := sender.nextPush()
				if got.ParentID != sent.ID || got.Body != "your user declined this request" {
					t.Fatalf("decline: %+v", got)
				}
			}
			if action == "R" {
				if _, err := sender.call("send", map[string]any{"to": id, "body": "again"}); err == nil {
					t.Fatal("never did not refuse")
				}
			}
		})
	}
}
