package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO's model list is one setting, and the settings read returns it.
func TestCTOSettingsCarryTheOrderedModelList(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour})
	cli := cliClient(t, n)
	models := []protocol.RoleModel{
		{Harness: "opencode", Provider: "fake", Model: "fake/m1", Effort: "high"},
		{Harness: "claude", Provider: "anthropic", Model: "opus"},
	}
	cli.mustCall(protocol.OpCTOSettings, protocol.CTOSettingsRequest{Models: &models})
	var got protocol.CTOSettings
	if err := json.Unmarshal(cli.mustCall(protocol.OpCTOSettings, nil), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 || got.Models[0] != models[0] || got.Models[1] != models[1] {
		t.Fatalf("models = %+v, want %+v", got.Models, models)
	}
	if got.Harness != "opencode" || got.Model != "fake/m1" {
		t.Fatalf("current choice = %s %s, want the first entry", got.Harness, got.Model)
	}
}

// A role start refused by a locked check returns one line naming the reason,
// so the TUI shows why the CTO did not start (decision 0162).
func TestCTOSendRefusalIsOneLine(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour})
	cli := cliClient(t, n)
	// agy is a known harness that this machine does not have installed.
	models := []protocol.RoleModel{{Harness: "agy", Model: "gemini-3-pro"}}
	cli.mustCall(protocol.OpCTOSettings, protocol.CTOSettingsRequest{Models: &models})
	_, err := cli.call(protocol.OpCTOSend, protocol.CTOSendRequest{Text: "hello"})
	if err == nil {
		t.Fatal("the CTO started on a harness this machine does not have")
	}
	text := err.Error()
	if strings.Contains(text, "\n") || !strings.Contains(text, "not installed") {
		t.Fatalf("refusal is not one reason line: %q", text)
	}
}

// A role resumes on the model it runs. When a locked check refuses that model,
// the CTO falls to the next entry of its list with a new conversation; it does
// not keep the old conversation on a model that cannot run (decision 0162).
func TestCTOResumeFallsToTheListWithANewConversation(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	sessions, _ := useFakeClaude(t)
	g := startGateway(t, t.TempDir(), "")
	gateCalls := 0
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
		c.Workers = shortSet
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, &fsClaudeAdapter{dir: sessions})
		c.HarnessPaths = map[string]string{"claude": os.Args[0]}
		c.testGatewayCall = func(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
			if typ != "worker.role.pick" {
				return nil, false, nil
			}
			gateCalls++
			req, _ := params.(protocol.RolePickRequest)
			if req.Resume {
				if req.Current.Harness != "claude" || req.Current.Model != "opus" {
					t.Errorf("resume asked for %+v, want the current claude opus", req.Current)
				}
				raw, err := json.Marshal(protocol.RolePickResult{Harness: "claude", Provider: "anthropic", Model: "sonnet", NewConversation: true})
				return raw, true, err
			}
			raw, err := json.Marshal(protocol.RolePickResult{Harness: "claude", Provider: "anthropic", Model: "opus"})
			return raw, true, err
		}
	}})
	ctx := context.Background()
	if _, err := n.d.ctoSend(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	first := readClaude(t, filepath.Join(n.cfg.StateDir, "cto", "claude"))
	if got := argValue(first.Args, "--model"); got != "opus" {
		t.Fatalf("first start model = %q, want opus", got)
	}
	n.d.cto.mu.Lock()
	n.d.stopClaudeCTOLocked("test")
	n.d.cto.mu.Unlock()
	if _, err := n.d.ctoSend(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	second := readClaude(t, filepath.Join(n.cfg.StateDir, "cto", "claude"))
	if got := argValue(second.Args, "--model"); got != "sonnet" {
		t.Fatalf("resumed model = %q, want the next list entry sonnet", got)
	}
	if got := argValue(second.Args, "--resume"); got != "" {
		t.Fatalf("resumed conversation %q; a locked refusal starts a new one", got)
	}
	if gateCalls < 2 {
		t.Fatalf("the gate was asked %d times, want a resume check too", gateCalls)
	}
}
