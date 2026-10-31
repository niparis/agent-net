package daemon

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A native entry with an allow-list gives the federated server the same list:
// the server reports more tools, but only the listed ones go through the
// gateway. An entry with no list records the tools the server reported.
func TestMCPImportAllowListIsKept(t *testing.T) {
	cmd := os.Args[0]
	codex := "[mcp_servers.listed]\ncommand = '" + cmd + "'\nargs = ['-test.run=XXX']\nenv_vars = ['" + helperEnv + "', 'GORACE']\nenabled_tools = ['echo']\n"
	f := newImportFixture(t, map[string]string{"open": stdioNative()}, codex)
	plan := f.plan(t, f.candidate(t, "codex", "listed"))
	if len(plan.Server.AllowedTools) != 1 || plan.Server.AllowedTools[0] != "echo" {
		t.Fatalf("plan allow-list: %+v", plan.Server.AllowedTools)
	}
	res := f.apply(t, plan.ID)
	if res.State != protocol.MCPImportOK || len(res.Tools) != 1 || res.Tools[0] != "echo" || !strings.Contains(res.Compared, "allow-list") {
		t.Fatalf("apply: %+v", res)
	}
	// The unlisted tools are real on the server, but not through the gateway.
	if out, err := callTool(t, f.cli, "listed", "echo", map[string]string{"text": "hi"}); err != nil || out.Text != "echo: hi" {
		t.Fatalf("listed tool: %+v %v", out, err)
	}
	if _, err := callTool(t, f.cli, "listed", "pid", map[string]string{}); err == nil || !strings.Contains(err.Error(), "not exposed") {
		t.Fatalf("unlisted tool: %v", err)
	}
	for _, s := range f.servers(t) {
		if s.Name == "listed" && toolNames(s) != "echo" {
			t.Fatalf("registry tools: %s", toolNames(s))
		}
	}
	// A later test or health report does not widen the list.
	f.cli.mustCall("mcp_test", protocol.MCPName{Name: "listed"})
	for _, s := range f.servers(t) {
		if s.Name == "listed" && toolNames(s) != "echo" {
			t.Fatalf("tools after mcp_test: %s", toolNames(s))
		}
	}

	// No allow-list: everything is accepted and the import record has the list.
	open := f.apply(t, f.plan(t, f.candidate(t, "claude", "open")).ID)
	if open.State != protocol.MCPImportOK || !strings.Contains(open.Compared, "not compared") {
		t.Fatalf("open: %+v", open)
	}
	var page protocol.ToolsPage
	if err := json.Unmarshal(f.cli.mustCall("tools_items", map[string]any{"kind": "mcp"}), &page); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, it := range page.Items {
		if it.Federated && it.Name == "open" {
			seen++
			if it.Imported == nil || len(it.Imported.Tools) < 2 || len(it.Imported.AllowedTools) != 0 {
				t.Fatalf("record without list: %+v", it.Imported)
			}
		}
		if it.Federated && it.Name == "listed" {
			seen++
			if it.Imported == nil || len(it.Imported.AllowedTools) != 1 || len(it.Imported.Tools) < 2 {
				t.Fatalf("record with list: %+v", it.Imported)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("federated items seen: %d", seen)
	}
}
