package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

type importFixture struct {
	g      *testGateway
	n      *node
	cli    *localClient
	home   string
	repo   string
	hashes map[string][32]byte
}

// newImportFixture starts a gateway and one host node whose fake home holds
// native MCP configs: Claude user and project entries and a Codex entry.
func newImportFixture(t *testing.T, claude map[string]string, codex string) *importFixture {
	t.Helper()
	return newImportFixtureWith(t, claude, codex, nil)
}

// newImportFixtureWith is newImportFixture with a fake harness adapter on the node.
func newImportFixtureWith(t *testing.T, claude map[string]string, codex string, adapter *fakeAdapter) *importFixture {
	t.Helper()
	useHelper(t)
	f := &importFixture{home: t.TempDir(), repo: filepath.Join(t.TempDir(), "alpha"), hashes: map[string][32]byte{}}
	if err := os.MkdirAll(f.repo, 0o700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, f.repo, "init", "-q", "-b", "main")
	servers := []string{}
	for name, def := range claude {
		servers = append(servers, `"`+name+`":`+def)
	}
	files := map[string]string{
		".claude.json":                     `{"mcpServers":{` + strings.Join(servers, ",") + `}}`,
		filepath.Join(f.repo, ".mcp.json"): `{"mcpServers":{"projsrv":` + stdioNative() + `}}`,
	}
	if codex != "" {
		files[".codex/config.toml"] = codex
	}
	for name, data := range files {
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(f.home, name)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		b := []byte(data)
		f.hashes[path] = sha256.Sum256(b)
	}
	f.g = startGateway(t, t.TempDir(), "")
	f.n = startNode(t, f.g, nodeOpts{adapter: adapter, cmdline: "pi", cfg: func(c *Config) { c.InventoryHome = f.home; c.CodexHome = filepath.Join(f.home, ".codex") }})
	f.cli = cliClient(t, f.n)
	f.cli.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{f.repo}})
	f.cli.mustCall("inventory_scan", nil)
	eventually(t, "native entries reach the gateway", func() bool {
		return len(f.candidates(t).Candidates) >= len(claude)+1+strings.Count(codex, "[mcp_servers.")
	})
	return f
}

// stdioNative is a native entry that runs the test binary as an MCP server.
func stdioNative() string {
	b, _ := json.Marshal(map[string]any{"command": os.Args[0], "args": []string{"-test.run=XXX"},
		"env": map[string]string{helperEnv: "${" + helperEnv + "}"}})
	return string(b)
}

func (f *importFixture) candidates(t *testing.T) protocol.MCPImportCandidatesPage {
	t.Helper()
	var page protocol.MCPImportCandidatesPage
	if err := json.Unmarshal(f.cli.mustCall("mcp_import_candidates", protocol.MCPImportCandidatesRequest{}), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func (f *importFixture) candidate(t *testing.T, harness, name string) protocol.MCPImportCandidate {
	t.Helper()
	for _, c := range f.candidates(t).Candidates {
		if c.Harness == harness && c.Name == name {
			return c
		}
	}
	t.Fatalf("no candidate %s %s", harness, name)
	return protocol.MCPImportCandidate{}
}

func (f *importFixture) plan(t *testing.T, c protocol.MCPImportCandidate) protocol.MCPImportPlan {
	t.Helper()
	var p protocol.MCPImportPlan
	if err := json.Unmarshal(f.cli.mustCall("mcp_import", protocol.MCPImportRequest{Candidate: c.Key}), &p); err != nil || p.ID == "" {
		t.Fatalf("plan: %+v %v", p, err)
	}
	return p
}

func (f *importFixture) apply(t *testing.T, id string) protocol.MCPImportResult {
	t.Helper()
	var r protocol.MCPImportResult
	if err := json.Unmarshal(f.cli.mustCall("mcp_import", protocol.MCPImportRequest{PlanID: id}), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// requireConfigsUntouched fails if any harness config file changed.
func (f *importFixture) requireConfigsUntouched(t *testing.T) {
	t.Helper()
	for path, sum := range f.hashes {
		b, err := os.ReadFile(path)
		if err != nil || sha256.Sum256(b) != sum {
			t.Errorf("%s was modified", path)
		}
	}
}

func (f *importFixture) servers(t *testing.T) []protocol.MCPServer { return listServers(t, f.cli) }

func TestMCPImportUserEntryEndToEnd(t *testing.T) {
	f := newImportFixture(t, map[string]string{"probe": stdioNative(), "secretive": `{"command":"/bin/true","env":{"API_TOKEN":"literal-secret-value"}}`}, "")
	cand := f.candidate(t, "claude", "probe")
	if cand.Class != protocol.MCPImportable || cand.Scope != protocol.InventoryUser {
		t.Fatalf("candidate: %+v", cand)
	}
	if s := f.candidate(t, "claude", "secretive"); s.Class != protocol.MCPUnsupported || !strings.Contains(s.Reason, "inline") {
		t.Fatalf("secret entry must stay unsupported: %+v", s)
	}
	raw := f.cli.mustCall("mcp_import_candidates", protocol.MCPImportCandidatesRequest{})
	if strings.Contains(string(raw), "literal-secret-value") || len(raw) > 20<<10 {
		t.Fatalf("candidate list leaks a value or is too big (%d bytes)", len(raw))
	}
	if _, err := f.cli.call("mcp_import", protocol.MCPImportRequest{Candidate: f.candidate(t, "claude", "secretive").Key}); err == nil || !strings.Contains(err.Error(), "cannot be imported") {
		t.Fatalf("unsupported entry planned: %v", err)
	}

	plan := f.plan(t, cand)
	if len(plan.Conflicts) != 0 || plan.Assignment.Scope != "global" || plan.Server.Name != "probe" || plan.Server.Enabled {
		t.Fatalf("plan: %+v", plan)
	}
	if len(f.servers(t)) != 0 {
		t.Fatal("planning registered a server")
	}
	res := f.apply(t, plan.ID)
	if res.State != protocol.MCPImportOK || res.Assignment.Scope != "global" || !res.NativeStillPresent || len(res.Tools) == 0 {
		t.Fatalf("apply: %+v", res)
	}
	srvs := f.servers(t)
	if len(srvs) != 1 || srvs[0].Name != "probe" || !srvs[0].Enabled || srvs[0].Health != protocol.HealthOK || srvs[0].Scope != "global" {
		t.Fatalf("registry: %+v", srvs)
	}
	// The federated server answers a call.
	if out, err := callTool(t, f.cli, "probe", "echo", map[string]string{"text": "hi"}); err != nil || out.Text != "echo: hi" {
		t.Fatalf("call: %+v %v", out, err)
	}
	// The item says where it came from and that the native entry remains, and
	// the same name is reported as a conflict, not shown as one server.
	var page protocol.ToolsPage
	if err := json.Unmarshal(f.cli.mustCall("tools_items", map[string]any{"kind": "mcp"}), &page); err != nil {
		t.Fatal(err)
	}
	var fed *protocol.ToolItem
	for i := range page.Items {
		if page.Items[i].Federated && page.Items[i].Name == "probe" {
			fed = &page.Items[i]
		}
	}
	if fed == nil || fed.Imported == nil || !fed.NativeStillPresent || len(fed.Conflicts) != 1 || fed.Imported.Harness != "claude" {
		t.Fatalf("federated item: %+v", fed)
	}
	if after := f.candidate(t, "claude", "probe"); !after.Imported || !after.NativeStillPresent {
		t.Fatalf("candidate after import: %+v", after)
	}
	hist := string(f.cli.mustCall("tools_history", protocol.ToolHistoryRequest{Item: fed.ID}))
	if !strings.Contains(hist, `"action":"mcp_import"`) || strings.Contains(hist, os.Args[0]) {
		t.Fatalf("history: %s", hist)
	}
	// A plan applies once.
	if _, err := f.cli.call("mcp_import", protocol.MCPImportRequest{PlanID: plan.ID}); err == nil {
		t.Fatal("a plan applied twice")
	}
	f.requireConfigsUntouched(t)
}

func TestMCPImportProjectEntryTakesProjectScope(t *testing.T) {
	f := newImportFixture(t, map[string]string{"probe": stdioNative()}, "")
	cand := f.candidate(t, "claude", "projsrv")
	if cand.Scope != protocol.InventoryProject || cand.Project != "alpha" {
		t.Fatalf("candidate: %+v", cand)
	}
	plan := f.plan(t, cand)
	if plan.Assignment.Scope != "projects" || len(plan.Assignment.Projects) != 1 || plan.Assignment.Projects[0] != "alpha" {
		t.Fatalf("plan scope: %+v", plan.Assignment)
	}
	if res := f.apply(t, plan.ID); res.State != protocol.MCPImportOK || res.Assignment.Scope != "projects" {
		t.Fatalf("apply: %+v", res)
	}
	for _, s := range f.servers(t) {
		if s.Name == "projsrv" && (s.Scope != "projects" || len(s.Projects) != 1 || s.Projects[0] != "alpha") {
			t.Fatalf("scope: %+v", s)
		}
	}
	f.requireConfigsUntouched(t)
}

func TestMCPImportFailedTestChangesNothing(t *testing.T) {
	broken := `{"command":"/nonexistent/agent-net-test-binary","args":["--serve"]}`
	f := newImportFixture(t, map[string]string{"broken": broken, "probe": stdioNative()}, "")
	cand := f.candidate(t, "claude", "broken")
	if cand.Class != protocol.MCPImportable {
		t.Fatalf("a missing command is found by the test, not the scan: %+v", cand)
	}
	plan := f.plan(t, cand)
	res := f.apply(t, plan.ID)
	if res.State != protocol.MCPImportFailed || !strings.Contains(res.Reason, "test") {
		t.Fatalf("apply: %+v", res)
	}
	if len(f.servers(t)) != 0 {
		t.Fatalf("a failed import left a server: %+v", f.servers(t))
	}
	var page protocol.ToolsPage
	if err := json.Unmarshal(f.cli.mustCall("tools_items", map[string]any{"kind": "mcp"}), &page); err != nil {
		t.Fatal(err)
	}
	for _, it := range page.Items {
		if it.Federated {
			t.Fatalf("a failed import left an item: %+v", it)
		}
	}
	if after := f.candidate(t, "claude", "broken"); after.Imported {
		t.Fatalf("candidate marked imported: %+v", after)
	}
	if _, err := f.cli.call("mcp_import", protocol.MCPImportRequest{PlanID: plan.ID}); err == nil {
		t.Fatal("a failed plan can be replayed")
	}
	f.requireConfigsUntouched(t)
}

// A native entry that lists tools the server does not report is not imported.
func TestMCPImportComparesTheToolList(t *testing.T) {
	cmd := os.Args[0]
	codex := "[mcp_servers.listed]\ncommand = '" + cmd + "'\nargs = ['-test.run=XXX']\nenv_vars = ['" + helperEnv + "']\nenabled_tools = ['echo']\n" +
		"[mcp_servers.wrong]\ncommand = '" + cmd + "'\nargs = ['-test.run=XXX']\nenv_vars = ['" + helperEnv + "']\nenabled_tools = ['echo','no_such_tool']\n"
	f := newImportFixture(t, map[string]string{"probe": stdioNative()}, codex)
	good := f.apply(t, f.plan(t, f.candidate(t, "codex", "listed")).ID)
	if good.State != protocol.MCPImportOK || !strings.Contains(good.Compared, "present") {
		t.Fatalf("listed: %+v", good)
	}
	bad := f.apply(t, f.plan(t, f.candidate(t, "codex", "wrong")).ID)
	if bad.State != protocol.MCPImportFailed || !strings.Contains(bad.Reason, "no_such_tool") {
		t.Fatalf("wrong: %+v", bad)
	}
	for _, s := range f.servers(t) {
		if s.Name == "wrong" {
			t.Fatal("a server with missing tools stayed registered")
		}
	}
	f.requireConfigsUntouched(t)
}

func TestMCPImportOwnerGate(t *testing.T) {
	f := newImportFixture(t, map[string]string{"probe": stdioNative()}, "")
	cand := f.candidate(t, "claude", "probe")
	agent, _ := helloPi(t, f.n, "agent-import", "/work", true)
	if _, err := agent.call("mcp_import_candidates", protocol.MCPImportCandidatesRequest{}); err == nil {
		t.Fatal("an agent read the candidates")
	}
	if _, err := agent.call("mcp_import", protocol.MCPImportRequest{Candidate: cand.Key}); err == nil {
		t.Fatal("an agent planned an import")
	}
	plan := f.plan(t, cand)
	if _, err := agent.call("mcp_import", protocol.MCPImportRequest{PlanID: plan.ID}); err == nil {
		t.Fatal("an agent applied an import")
	}
	if len(f.servers(t)) != 0 {
		t.Fatal("a refused request registered a server")
	}
	// The CTO tools go through the same owner path and are recorded as the CTO.
	v, err := f.n.d.runCTOToolRaw(context.Background(), "mcp_import", importJSON(t, protocol.MCPImportRequest{PlanID: plan.ID}))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(v); !strings.Contains(string(b), `"state":"imported"`) {
		t.Fatalf("cto apply: %s", b)
	}
	f.requireConfigsUntouched(t)
}

func importJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A plan binds to what was scanned: a changed native entry makes it stale.
func TestMCPImportPlanGoesStaleWhenTheEntryChanges(t *testing.T) {
	f := newImportFixture(t, map[string]string{"probe": stdioNative()}, "")
	plan := f.plan(t, f.candidate(t, "claude", "probe"))
	changed := `{"mcpServers":{"probe":` + strings.Replace(stdioNative(), "-test.run=XXX", "-test.run=YYY", 1) + `}}`
	if err := os.WriteFile(filepath.Join(f.home, ".claude.json"), []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	f.cli.mustCall("inventory_scan", nil)
	if _, err := f.cli.call("mcp_import", protocol.MCPImportRequest{PlanID: plan.ID}); err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("stale plan: %v", err)
	}
	if len(f.servers(t)) != 0 {
		t.Fatal("a stale plan registered a server")
	}
}

// A name that a federated server already holds is a conflict; the plan cannot
// be applied.
func TestMCPImportRefusesADuplicateName(t *testing.T) {
	f := newImportFixture(t, map[string]string{"probe": stdioNative()}, "")
	f.cli.mustCall("mcp_add", protocol.MCPAdd{Server: stdioSpec("probe")})
	cand := f.candidate(t, "claude", "probe")
	if !strings.Contains(cand.Conflict, "already exists") {
		t.Fatalf("candidate conflict: %+v", cand)
	}
	plan := f.plan(t, cand)
	if len(plan.Conflicts) == 0 {
		t.Fatalf("plan has no conflict: %+v", plan)
	}
	if _, err := f.cli.call("mcp_import", protocol.MCPImportRequest{PlanID: plan.ID}); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("apply: %v", err)
	}
	if n := len(f.servers(t)); n != 1 {
		t.Fatalf("registry changed: %d servers", n)
	}
}
