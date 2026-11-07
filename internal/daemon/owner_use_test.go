package daemon

import (
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

func TestOwnerUseEnter(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n, _, sid := enterNode(t, g, "opencode", protocol.SessionInfo{NativeID: "entry", Cwd: t.TempDir(), Status: "idle"}, fakeBin(t, "opencode"), nil)
	p := enterOf(t, n, sid, "")
	if len(p.Argv) == 0 {
		t.Fatalf("no attach: %+v", p)
	}
	a, _, err := g.gw.Store().Agent(sid)
	if err != nil || a.OwnerUses != 1 {
		t.Fatalf("enter count: %+v %v", a, err)
	}
}
