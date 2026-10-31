package daemon

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSignalPolicyHookAndSend(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/claude", interval: time.Hour}, nil)
	n.d.SetSessions("claude", []protocol.SessionInfo{{NativeID: "owner-turn", Name: "sovl", Repo: protocol.RepoInfo{Project: "sovl-clone"}}})
	eventually(t, "Claude session", func() bool { return n.d.SessionID("claude", "owner-turn") != "" })
	sender := n.d.SessionID("claude", "owner-turn")
	st := g.gw.Store()
	node, _, err := st.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := st.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "peer", Name: "homelab", Repo: protocol.RepoInfo{Project: "homelab"}})
	if err != nil {
		t.Fatal(err)
	}
	cli := cliClient(t, n)
	askPolicy(t, cli, protocol.PolicyRequest{Harness: "claude", NativeID: "owner-turn", Prompt: "ask homelab about github tokens"})
	m, _, err := st.StoreMessage(protocol.MsgSend{Sender: sender, Recipient: recipient, Body: "please"})
	if err != nil {
		t.Fatal(err)
	}
	signals, err := st.Signals()
	if err != nil || len(signals) != 1 || signals[0].MessageID != m.ID {
		t.Fatalf("signals %+v: %v", signals, err)
	}
}
