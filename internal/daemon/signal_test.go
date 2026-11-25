package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// signalRig starts a gateway with one Claude session named owner-turn and one
// peer session named homelab. It returns the gateway, its node, the Claude
// client, and the two session IDs.
func signalRig(t *testing.T) (*testGateway, *node, *localClient, string, string) {
	t.Helper()
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
	return g, n, cliClient(t, n), sender, recipient
}

func TestSignalPolicyHookAndSend(t *testing.T) {
	t.Parallel()
	g, _, cli, sender, recipient := signalRig(t)
	transcript := writeClaudeTranscript(t, t.TempDir(), "owner-turn",
		claudeUserEntry("p-signal", "ask homelab about github tokens", typedOrigin))
	askPolicy(t, cli, protocol.PolicyRequest{Harness: "claude", NativeID: "owner-turn", Prompt: "ask homelab about github tokens",
		PromptID: "p-signal", TranscriptPath: transcript})
	st := g.gw.Store()
	m, _, err := st.StoreMessage(protocol.MsgSend{Sender: sender, Recipient: recipient, Body: "please"})
	if err != nil {
		t.Fatal(err)
	}
	signals, err := st.Signals()
	if err != nil || len(signals) != 1 || signals[0].MessageID != m.ID {
		t.Fatalf("signals %+v: %v", signals, err)
	}
}

// No transcript entry proves the owner typed the prompt, so it is not kept as
// his words. The next message carries no signal. A late human entry is covered
// in claude_sender_test.go.
func TestSignalPolicyHookWithoutTranscriptSendsNoSignal(t *testing.T) {
	t.Parallel()
	g, n, cli, sender, recipient := signalRig(t)
	n.d.claudeOwnerWait = 100 * time.Millisecond
	askPolicy(t, cli, protocol.PolicyRequest{Harness: "claude", NativeID: "owner-turn", Prompt: "ask homelab about github tokens",
		PromptID: "p-signal-none", TranscriptPath: filepath.Join(t.TempDir(), "owner-turn.jsonl")})
	n.d.claudeChecks.Wait()
	st := g.gw.Store()
	if _, _, err := st.StoreMessage(protocol.MsgSend{Sender: sender, Recipient: recipient, Body: "please"}); err != nil {
		t.Fatal(err)
	}
	signals, err := st.Signals()
	if err != nil || len(signals) != 0 {
		t.Fatalf("signals %+v: %v", signals, err)
	}
}
