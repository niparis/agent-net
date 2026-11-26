package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

type permissionSettings struct {
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
	} `json:"permissions"`
	Other string `json:"other"`
}

func readPermissionSettings(t *testing.T, repo string) permissionSettings {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var doc permissionSettings
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("settings are not JSON: %v\n%s", err, raw)
	}
	return doc
}

func countString(list []string, want string) int {
	n := 0
	for _, v := range list {
		if v == want {
			n++
		}
	}
	return n
}

// An unregistered target is refused at proposal time and named, before any ask
// is opened.
func TestPermissionProposalRejectsUnregisteredTarget(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	cli := cliClient(t, n)
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	cli.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{repo}})
	pi, _ := helloPi(t, n, "np", repo, false)

	_, err := pi.call(protocol.OpPermissionPropose, protocol.PermissionProposeRequest{
		Rules: []string{"Bash(gh pr merge *)"}, Targets: []string{"missing-repo"}, Reason: "merge PRs",
	})
	if err == nil || !strings.Contains(err.Error(), "missing-repo") {
		t.Fatalf("unregistered target: %v", err)
	}
	var inbox struct {
		Items []protocol.InboxItem `json:"items"`
	}
	if err := json.Unmarshal(cli.mustCall("inbox_list", nil), &inbox); err != nil {
		t.Fatal(err)
	}
	for _, item := range inbox.Items {
		if item.Kind == protocol.AskKindPermission {
			t.Fatalf("an ask was opened for a refused target: %+v", item)
		}
	}
}

// A session whose role node holds the permissions scope over its repo has the
// proposal approved by that scope: the rules reach the repo's settings file at
// once, the proposal is decided by the scope, and the owner gets no ask.
func TestPermissionProposalFromAScopeHolderAppliesWithoutAnAsk(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	if _, err := n.d.call(context.Background(), "hello", map[string]any{"hostname": "fixture", "version": "v0.4.145", "graph_refs": true}); err != nil {
		t.Fatal(err)
	}
	waitGraphCutover(t, g)
	cli := cliClient(t, n)
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	name := filepath.Base(repo)
	cli.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{repo}})
	pi, piSession := helloPi(t, n, "np", repo, false)
	st := g.gw.Store()
	view, ok, err := st.Agent(piSession)
	if err != nil || !ok || view.AgentID == "" {
		t.Fatalf("the session has no role node: %+v %v", view, err)
	}
	if _, err := st.SetScope(protocol.ScopeSetRequest{Node: view.AgentID, Kind: protocol.ScopePermissions, Over: "repo:" + name}, "owner", false); err != nil {
		t.Fatal(err)
	}
	propose := func(rule string) protocol.PermissionProposeResult {
		var res protocol.PermissionProposeResult
		raw := pi.mustCall(protocol.OpPermissionPropose, protocol.PermissionProposeRequest{Rules: []string{rule}, Targets: []string{name}, Reason: "merge PRs"})
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := propose("Bash(gh pr merge:*)")
	if res.Status != "applied" {
		t.Fatalf("proposal: %+v", res)
	}
	if got := readPermissionSettings(t, repo); countString(got.Permissions.Allow, "Bash(gh pr merge:*)") != 1 {
		t.Fatalf("settings after the scope approved it: %+v", got)
	}
	for _, it := range func() []protocol.InboxItem { items, _ := st.ListInbox(); return items }() {
		if it.Kind == protocol.AskKindPermission && it.Status == "open" {
			t.Fatalf("the owner got an ask: %+v", it)
		}
	}
	// A rule on the never list still waits for the owner.
	if res := propose("Bash(git push --force:*)"); res.Status != "pending" {
		t.Fatalf("a never-list rule: %+v", res)
	}
}

// A caller's own mcp_servers_by_path never reaches the owner's trust ask. The
// daemon fills that list from each folder's own .mcp.json on the machine that
// holds it, so a forged list is dropped and a folder with no server stays
// empty.
func TestPermissionProposeTrustIgnoresCallerMCPServers(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := folderTrustEnv(t)
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	plain := newHomeRepo(t, home, "code/plain")
	withServer := newHomeRepo(t, home, "code/with-server")
	if err := os.WriteFile(filepath.Join(withServer, ".mcp.json"),
		[]byte(`{"mcpServers":{"real":{"command":"real-mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pi, _ := helloPi(t, n, "np", home, false)

	propose := func(folder string) protocol.PermissionProposeResult {
		t.Helper()
		raw := pi.mustCall(protocol.OpPermissionPropose, protocol.PermissionProposeRequest{
			Kind:    protocol.PermissionKindTrust,
			Machine: n.cfg.Hostname,
			Targets: []string{folder},
			Reason:  "Claude Code cannot start here",
			// A caller that speaks the local protocol itself could send this.
			MCPServersByPath: map[string][]string{folder: {"forged-server"}},
		})
		var res protocol.PermissionProposeResult
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatal(err)
		}
		return res
	}
	plainRes := propose(plain)
	if len(plainRes.Targets) != 1 || len(plainRes.Targets[0].MCPServers) != 0 {
		t.Fatalf("a caller's server list reached the proposal: %+v", plainRes.Targets)
	}
	if res := propose(withServer); len(res.Targets) != 1 ||
		!reflect.DeepEqual(res.Targets[0].MCPServers, []string{"real"}) {
		t.Fatalf("the folder's own server did not fill the proposal: %+v", res.Targets)
	}
}

// mcpFolder makes home/rel an untrusted git folder whose own .mcp.json declares
// tenebrae-library, the tenebrae-strats case.
func mcpFolder(t *testing.T, home, rel string) string {
	t.Helper()
	repo := newHomeRepo(t, home, rel)
	commitMCPJSON(t, repo, map[string]any{"tenebrae-library": map[string]any{
		"command": "just", "args": []string{"library-mcp"},
	}})
	return repo
}

// trust true on the start is the owner's yes to the folder; it approves the
// folder's own .mcp.json servers in the same start.
func TestSessionStartTrustApprovesFolderMCPServers(t *testing.T) {
	t.Setenv(envFakeMCPBlocked, "1")
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	repo := mcpFolder(t, home, "code/tenebrae")

	r := sessionStartPathCall(t, c.workerNode, protocol.SessionStartRequest{Path: repo, Harness: "claude", TrustClaude: true})
	r = waitSessionStartPath(t, c.workerNode, r)
	t.Cleanup(func() { killFakeClaude(t, repo) })
	if r.Status != "started" || r.ErrorCode != "" {
		t.Fatalf("trusted start: %+v", r)
	}
	if got := readLocalSettings(t, repo); !reflect.DeepEqual(got["enabledMcpjsonServers"], []any{"tenebrae-library"}) {
		t.Fatalf("folder settings after the trusted start: %v", got)
	}
}

// An explicit mcp_servers disable given with the start still wins over the
// trust click's approval of the folder's own servers.
func TestSessionStartTrustKeepsExplicitMCPDisable(t *testing.T) {
	t.Setenv(envFakeMCPBlocked, "1")
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	repo := mcpFolder(t, home, "code/tenebrae")

	r := sessionStartPathCall(t, c.workerNode, protocol.SessionStartRequest{
		Path: repo, Harness: "claude", TrustClaude: true, MCPServers: protocol.MCPDisable,
	})
	r = waitSessionStartPath(t, c.workerNode, r)
	t.Cleanup(func() { killFakeClaude(t, repo) })
	if r.Status != "started" {
		t.Fatalf("start with disable: %+v", r)
	}
	got := readLocalSettings(t, repo)
	if !reflect.DeepEqual(got["disabledMcpjsonServers"], []any{"tenebrae-library"}) {
		t.Fatalf("disable did not win: %v", got)
	}
	if v, ok := got["enabledMcpjsonServers"]; ok && len(v.([]any)) != 0 {
		t.Fatalf("an explicit disable was overridden by the trust click: %v", got)
	}
}
