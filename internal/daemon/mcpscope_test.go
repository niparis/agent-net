package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func scopedCatalog(t *testing.T, lc *localClient) string {
	t.Helper()
	var r struct {
		Servers []protocol.MCPServer `json:"servers"`
	}
	if err := json.Unmarshal(lc.mustCall("mcp_tools", nil), &r); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range r.Servers {
		names = append(names, s.Name)
	}
	return strings.Join(names, ",")
}

// A server hosted on one machine is assigned to one project. Sessions of
// another project on a third machine stop seeing it, get a push to refresh,
// and a call they cached earlier is refused at the gateway. A request that
// names another session or project changes nothing.
func TestMCPScopedFederationEndToEnd(t *testing.T) {
	useHelper(t)
	g := startGateway(t, t.TempDir(), "")
	host := startNode(t, g, nodeOpts{cmdline: "pi"})
	work := startNode(t, g, nodeOpts{cmdline: "pi"})
	root := t.TempDir()
	repos := map[string]string{}
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, "init", "-q", "-b", "main")
		repos[name] = dir
	}
	cli := cliClient(t, work)
	cli.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{repos["alpha"], repos["beta"]}})
	alpha, alphaID := helloPi(t, work, "na", repos["alpha"], false)
	beta, _ := helloPi(t, work, "nb", repos["beta"], false)

	// Added before scoping existed, so it has no assignment: global.
	cliClient(t, host).mustCall("mcp_add", protocol.MCPAdd{Server: stdioSpec("probe")})
	waitPush(t, alpha, "mcp_changed")
	waitPush(t, beta, "mcp_changed")
	if a, b := scopedCatalog(t, alpha), scopedCatalog(t, beta); a != "probe" || b != "probe" {
		t.Fatalf("global server: alpha sees %q, beta sees %q", a, b)
	}
	if out, err := callTool(t, beta, "probe", "echo", map[string]string{"text": "hi"}); err != nil || out.Text != "echo: hi" {
		t.Fatalf("beta call before assignment: %+v %v", out, err)
	}

	// Assign it to alpha with the owner's plan and apply.
	var item protocol.ToolItem
	raw := cli.mustCall("tools_items", map[string]any{"kind": "mcp"})
	var page protocol.ToolsPage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	for _, it := range page.Items {
		if it.Name == "probe" {
			item = it
		}
	}
	if !item.Federated || item.Scope != "global" || item.Host == "" {
		t.Fatalf("listing: %+v", item)
	}
	var plan protocol.ToolPlan
	raw = cli.mustCall("tools_plan", protocol.ToolPlanRequest{Item: item.ID, Target: protocol.ToolAssignment{Scope: "projects", Projects: []string{"alpha"}}})
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	cli.mustCall("tools_apply", protocol.ToolPlanID{ID: plan.ID})

	// Live refresh: both sessions are told, and the catalogs differ.
	waitPush(t, alpha, "mcp_changed")
	waitPush(t, beta, "mcp_changed")
	if a, b := scopedCatalog(t, alpha), scopedCatalog(t, beta); a != "probe" || b != "" {
		t.Fatalf("after assignment: alpha sees %q, beta sees %q", a, b)
	}
	if out, err := callTool(t, alpha, "probe", "echo", map[string]string{"text": "ok"}); err != nil || out.Text != "echo: ok" {
		t.Fatalf("alpha call: %+v %v", out, err)
	}
	// beta cached the tool earlier: the gateway refuses the stale call.
	if _, err := callTool(t, beta, "probe", "echo", map[string]string{"text": "x"}); err == nil || !strings.Contains(err.Error(), "not assigned to this project") {
		t.Fatalf("stale call: %v", err)
	}
	// Claiming alpha's session or project does not help.
	_, err := beta.call("mcp_call", map[string]any{"server": "probe", "tool": "echo", "session_id": alphaID, "project": "alpha"})
	if err == nil || !strings.Contains(err.Error(), "not assigned to this project") {
		t.Fatalf("spoofed call: %v", err)
	}
	// A session cannot claim the owner's authority either: the owner ID in
	// its request is dropped by the daemon.
	eventually(t, "owner session", func() bool { return work.d.ownerSessionID() != "" })
	_, err = beta.call("mcp_call", map[string]any{"server": "probe", "tool": "echo", "owner": work.d.ownerSessionID()})
	if err == nil || !strings.Contains(err.Error(), "not assigned to this project") {
		t.Fatalf("owner claim from a session: %v", err)
	}
	// The owner's own command line calls with the owner's authority, whatever
	// the project.
	if out, err := callTool(t, cli, "probe", "echo", map[string]string{"text": "own"}); err != nil || out.Text != "echo: own" {
		t.Fatalf("owner command line call: %+v %v", out, err)
	}
	// A command line run from inside an agent session has no authority: it
	// sees only global servers.
	work.d.inSession = func(int) bool { return true }
	if _, err = callTool(t, cli, "probe", "echo", map[string]string{"text": "in"}); err == nil || !strings.Contains(err.Error(), "not assigned to this project") {
		t.Fatalf("command line inside a session: %v", err)
	}
	work.d.inSession = func(int) bool { return false }
	// The server's owner view lists the scope.
	for _, s := range listServers(t, cli) {
		if s.Name == "probe" && (s.Scope != "projects" || len(s.Projects) != 1 || s.Projects[0] != "alpha") {
			t.Fatalf("mcp_list scope: %+v", s)
		}
	}
}
