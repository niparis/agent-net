package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOSettingsOwnerGate(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi", exe: "/opt/pi"})
	pi, _ := helloPi(t, n, "p1", "/w/p", false)
	model := "provider/model"
	if _, err := pi.call(protocol.OpCTOSettings, protocol.CTOSettingsRequest{Model: &model}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Fatalf("agent: %v", err)
	}
	cli := cliClient(t, n)
	cli.mustCall(protocol.OpCTOSettings, protocol.CTOSettingsRequest{Model: &model})
	raw := cli.mustCall(protocol.OpCTOSettings, nil)
	var out protocol.CTOSettings
	if err := json.Unmarshal(raw, &out); err != nil || out.Model != model {
		t.Fatalf("settings: %+v %v", out, err)
	}
}

func TestCTOHarnessSettings(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi", exe: "/opt/pi"})
	cli := cliClient(t, n)
	var out protocol.CTOSettings
	raw := cli.mustCall(protocol.OpCTOSettings, nil)
	if err := json.Unmarshal(raw, &out); err != nil || out.Harness != "opencode" {
		t.Fatalf("default: %+v %v", out, err)
	}
	model := "opus"
	harness := "claude"
	cli.mustCall(protocol.OpCTOSettings, protocol.CTOSettingsRequest{Harness: &harness, Model: &model})
	raw = cli.mustCall(protocol.OpCTOSettings, nil)
	if err := json.Unmarshal(raw, &out); err != nil || out.Harness != harness || out.Model != model || out.ClaudeModel != model {
		t.Fatalf("settings: %+v %v", out, err)
	}
	bad := "other"
	if _, err := cli.call(protocol.OpCTOSettings, protocol.CTOSettingsRequest{Harness: &bad}); err == nil {
		t.Fatal("accepted unknown harness")
	}
}

// The CTO runs on Codex like on Claude and OpenCode; Pi is refused with the
// reason. Each harness keeps its own model across a switch.
func TestCTOSettingsCodexAndPi(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi", exe: "/opt/pi"})
	n.d.cfg.testHandoverWait = time.Millisecond
	cli := cliClient(t, n)
	set := func(harness, model string) protocol.CTOSettings {
		t.Helper()
		var out protocol.CTOSettings
		if err := json.Unmarshal(cli.mustCall(protocol.OpCTOSettings, protocol.CTOSettingsRequest{Harness: &harness, Model: &model}), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := set("codex", "gpt-fake:high"); out.Harness != "codex" || out.Model != "gpt-fake:high" || out.CodexModel != "gpt-fake:high" {
		t.Fatalf("codex: %+v", out)
	}
	set("claude", "opus")
	codex := "codex"
	var out protocol.CTOSettings
	if err := json.Unmarshal(cli.mustCall(protocol.OpCTOSettings, protocol.CTOSettingsRequest{Harness: &codex}), &out); err != nil {
		t.Fatal(err)
	}
	if out.Model != "gpt-fake:high" || out.ClaudeModel != "opus" {
		t.Fatalf("back to codex: %+v", out)
	}
	pi := "pi"
	if _, err := cli.call(protocol.OpCTOSettings, protocol.CTOSettingsRequest{Harness: &pi}); err == nil || !strings.Contains(err.Error(), "Pi cannot be woken by a message yet") {
		t.Fatalf("pi harness: %v", err)
	}
	models := []protocol.RoleModel{{Harness: "pi", Model: "x"}}
	if _, err := cli.call(protocol.OpCTOSettings, protocol.CTOSettingsRequest{Models: &models}); err == nil || !strings.Contains(err.Error(), "Pi cannot be woken by a message yet") {
		t.Fatalf("pi list: %v", err)
	}
}
