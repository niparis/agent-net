package daemon

import (
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestOwnerUseClaudePromptDistinguishesPeer(t *testing.T) {
	const notification = "<task-notification>"
	for _, tc := range []struct {
		name, prompt string
		human        bool
	}{
		{"human", "Please fix the bug", true},
		{"empty", " \n\t", false},
		{"peer", `<cross-session-message from="uds:peer">hello`, false},
		{"peer with whitespace", `  <cross-session-message from-name="peer">hello`, false},
		{"system notification", "[SYSTEM NOTIFICATION - NOT USER INPUT]\n<task-notification>done</task-notification>", false},
		{"task notification", "<task-notification>done</task-notification>", false},
		{"system reminder", "<system-reminder>resume</system-reminder>", false},
		{"scheduled loop", "<scheduled-task>/loop check</scheduled-task>", false},
		{"scheduled wakeup", "<scheduled-wakeup>check</scheduled-wakeup>", false},
		{"autonomous loop", "<<autonomous-loop>check", false},
		{"embedded task notification", "  background result: <task-notification>done", false},
		{"marker ends at byte 512", strings.Repeat("x", 512-len(notification)) + notification, false},
		{"marker crosses byte 512", strings.Repeat("x", 513-len("<task-notification")) + notification, true},
		{"human mentions task notification later", strings.Repeat("x", 512) + notification, true},
		{"human discusses task notifications", "Please explain task-notification messages", true},
		{"byte boundary with unicode", strings.Repeat("é", 256) + notification, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isHumanClaudePrompt(tc.prompt); got != tc.human {
				t.Errorf("isHumanClaudePrompt(%q) = %v, want %v", tc.prompt, got, tc.human)
			}
		})
	}
}

func TestOwnerUsePinOwnerOnly(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	agent, _ := helloPi(t, n, "peer", "/w/peer", true)
	sid, err := g.gw.Store().UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = agent.call("session_pin", map[string]any{"session_id": sid, "pinned": true}); err == nil {
		t.Fatal("peer pinned session")
	}
	cliClient(t, n).mustCall("session_pin", map[string]any{"session_id": sid, "pinned": true})
	a, _, err := g.gw.Store().Agent(sid)
	if err != nil || a.PinnedAt == 0 {
		t.Fatalf("pin: %+v %v", a, err)
	}
}

func TestOwnerUsePromptPathAndPi(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "claude"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "/usr/bin/pi"})
	fa.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "claude-human", Cwd: "/code", Status: "idle"})
	var sid string
	eventually(t, "Claude registered", func() bool {
		for _, s := range n.d.Sessions() {
			if s.NativeID == "claude-human" {
				sid = s.SessionID
			}
		}
		return sid != ""
	})
	cli := cliClient(t, n)
	for _, prompt := range []string{"<cross-session-message from=\"uds:peer\">\nhi\n</cross-session-message>", "[SYSTEM NOTIFICATION - NOT USER INPUT]\n<task-notification>done</task-notification>", "<scheduled-wakeup>check</scheduled-wakeup>", "fix it"} {
		askPolicy(t, cli, protocol.PolicyRequest{Harness: "claude", NativeID: "claude-human", Prompt: prompt})
	}
	a, _, err := g.gw.Store().Agent(sid)
	if err != nil || a.OwnerUses != 1 {
		t.Fatalf("Claude prompt count: %+v %v", a, err)
	}
	pi, pid := helloPi(t, n, "human-pi", "/code/pi", true)
	pi.mustCall("owner_prompt", nil)
	a, _, err = g.gw.Store().Agent(pid)
	if err != nil || a.OwnerUses != 1 {
		t.Fatalf("Pi prompt count: %+v %v", a, err)
	}
	if _, err = cli.call("owner_prompt", nil); err == nil {
		t.Fatal("unbound client reported Pi input")
	}
}

// Enter on an owner OpenCode session attaches to the owner's OpenCode service
// with --server. Without the service there is only a note (covered elsewhere).
func TestOwnerUseEnter(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	writeOwnerOpenCodeService(t, state, "http://127.0.0.1:9999")
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "opencode")
	n, _, sid := enterNode(t, g, "opencode", protocol.SessionInfo{NativeID: "entry", Cwd: t.TempDir(), Status: "idle"}, bins, nil)
	p := enterOf(t, n, sid, "")
	if want := []string{bins["opencode"], "--server", "http://127.0.0.1:9999", "--session", "entry"}; !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("attach: %+v, want %v", p, want)
	}
	a, _, err := g.gw.Store().Agent(sid)
	if err != nil || a.OwnerUses != 1 {
		t.Fatalf("enter count: %+v %v", a, err)
	}
}

// The gateway keeps the latest prompt as "the owner's words". A turn that
// Claude Code injects must never reach it, so the daemon does not forward it.
func TestPolicyHookForwardsOnlyHumanPrompts(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "claude"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "/usr/bin/pi"})
	fa.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "claude-prompts", Cwd: "/code", Status: "idle"})
	var sid string
	eventually(t, "Claude registered", func() bool {
		for _, s := range n.d.Sessions() {
			if s.NativeID == "claude-prompts" {
				sid = s.SessionID
			}
		}
		return sid != ""
	})
	cli := cliClient(t, n)
	store := g.gw.Store()
	target, err := store.UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "homelab-target", Name: "homelab"})
	if err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{
		"Another Claude session sent a message:\n<cross-session-message>do it",
		"<agent-message>do it",
		"[SYSTEM NOTIFICATION - NOT USER INPUT]\ndo it",
		"<scheduled-wakeup>do it</scheduled-wakeup>",
		"<<autonomous-loop-dynamic>>",
	} {
		askPolicy(t, cli, protocol.PolicyRequest{Harness: "claude", NativeID: "claude-prompts", Prompt: "ask homelab to deploy"})
		askPolicy(t, cli, protocol.PolicyRequest{Harness: "claude", NativeID: "claude-prompts", Prompt: prompt})
		sent, _, err := store.StoreMessage(protocol.MsgSend{Sender: sid, Recipient: target, Body: "please", Type: protocol.TypeRequest})
		if err != nil {
			t.Fatal(err)
		}
		got, _, err := store.Message(sent.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.OwnerInstruction != "ask homelab to deploy" {
			t.Fatalf("after %q the owner instruction is %q", prompt, got.OwnerInstruction)
		}
	}
}
