package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A project with no bar asks its lead once. The lead writes the bar with
// agent_bar_edit, and the daemon then puts it into the brief of the next
// worker of that project. The command line cannot write it.
func TestBarRequestEditAndBriefReachTheDaemon(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	repo := newRepo(t, "demo")
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# Project: demo\n\nPerformance first.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, id := helloDelivery(t, n, "caller", repo, "claude-opus-4")
	eventually(t, "session and repo registered", func() bool {
		a, ok, err := g.gw.Store().Agent(id)
		return err == nil && ok && a.Repo.Project == "demo"
	})
	if err := g.gw.Store().SetCharter("demo", "maturity", "early", "owner"); err != nil {
		t.Fatal(err)
	}
	first := askPolicy(t, client, protocol.PolicyRequest{})
	for _, want := range []string{"Project rules request", "agent_bar_edit"} {
		if !strings.Contains(first.Charter, want) {
			t.Fatalf("first turn lacks %q:\n%s", want, first.Charter)
		}
	}
	if brief := protocol.BarBriefSection(n.d.barOf(context.Background(), "demo")); brief != "" {
		t.Fatalf("brief section before any rule:\n%s", brief)
	}

	cli := cliClient(t, n)
	if _, err := cli.call(protocol.OpBarEdit, protocol.BarEditRequest{Project: "demo", Add: []string{"x"}}); err == nil {
		t.Fatal("the command line wrote the bar")
	}
	var res protocol.BarEditResult
	raw := client.mustCall(protocol.OpBarEdit, protocol.BarEditRequest{Add: []string{"Performance first.", "No scratch files."}})
	if err := json.Unmarshal(raw, &res); err != nil || len(res.Rules) != 2 || res.Rules[0].ID != "B1" {
		t.Fatalf("edit result: %s %v", raw, err)
	}
	eventually(t, "the daemon reads the bar", func() bool { return len(n.d.barOf(context.Background(), "demo")) == 2 })
	body := protocol.WorkerTaskBodyBar("opencode", "fix it", "", "", "", "", protocol.WorkersSandboxed, "", n.d.SettingsSnapshot(), "demo", n.d.barOf(context.Background(), "demo"))
	if !strings.Contains(body, "\n- B1: Performance first.\n- B2: No scratch files.") {
		t.Fatalf("brief:\n%s", body)
	}
}

// The CTO edits a project's bar with cto_op bar_edit, through the same gateway
// frame as agent_bar_edit. The owner method is not an agent tool: an agent
// session that calls it directly is refused and the bar is unchanged.
func TestCTOOpBarEditWritesTheBarAndAgentsCannotCallIt(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	repo := newRepo(t, "demo")
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# Project: demo\n\nPerformance first.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, id := helloDelivery(t, n, "caller", repo, "claude-opus-4")
	eventually(t, "session and repo registered", func() bool {
		a, ok, err := g.gw.Store().Agent(id)
		return err == nil && ok && a.Repo.Project == "demo"
	})
	if err := g.gw.Store().SetCharter("demo", "maturity", "early", "owner"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"method": "bar_edit", "args": map[string]any{"project": "demo", "add": []string{"Performance first."}}})
	if _, err := n.d.runCTOTool(ctx, "cto_op", raw); err != nil {
		t.Fatalf("cto_op bar_edit: %v", err)
	}
	eventually(t, "the daemon reads the CTO's bar", func() bool { return len(n.d.barOf(ctx, "demo")) == 1 })

	if _, err := client.call("bar_edit", protocol.BarEditRequest{Project: "demo", Add: []string{"Injected."}}); err == nil {
		t.Fatal("an agent session called the owner method bar_edit")
	}
	if got := n.d.barOf(ctx, "demo"); len(got) != 1 || got[0].Text != "Performance first." {
		t.Fatalf("bar after a refused call: %+v", got)
	}
}
