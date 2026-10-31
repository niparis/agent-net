package daemon

import (
	"encoding/json"
	"strings"
	"testing"

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
