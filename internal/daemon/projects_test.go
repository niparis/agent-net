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

// codeDir makes a tree like ~/code: repos alpha, beta and group/gamma, a
// linked worktree of alpha, a repo inside node_modules, and a folder that is
// not a repo. The caller gets the real (symlink free) path.
func codeDir(t *testing.T) string {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	for _, rel := range []string{"alpha", "beta", "group/gamma", "node_modules/dep"} {
		dir := filepath.Join(base, rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, "init", "-q", "-b", "main")
		gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	}
	gitIn(t, filepath.Join(base, "alpha"), "remote", "add", "origin", "https://bob:hunter2@example.com/o/alpha.git")
	gitIn(t, filepath.Join(base, "alpha"), "worktree", "add", "-q", "-b", "task", filepath.Join(base, "alpha-task"))
	if err := os.MkdirAll(filepath.Join(base, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	return base
}

func addOutcome(t *testing.T, raw json.RawMessage) protocol.ProjectAddOutcome {
	t.Helper()
	var o protocol.ProjectAddOutcome
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatal(err)
	}
	return o
}

func listProjects(t *testing.T, lc *localClient, name string) []protocol.ProjectView {
	t.Helper()
	var r struct {
		Projects []protocol.ProjectView `json:"projects"`
	}
	if err := json.Unmarshal(lc.mustCall("projects", protocol.QueryProjects{Name: name}), &r); err != nil {
		t.Fatal(err)
	}
	return r.Projects
}

func names(es []protocol.ProjectEntry) string {
	var n []string
	for _, e := range es {
		n = append(n, e.Name)
	}
	return strings.Join(n, ",")
}

func TestProjectScanDryRunScanAndRerun(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	base := codeDir(t)
	n := startNode(t, g, nodeOpts{})
	cli := cliClient(t, n)

	dry := addOutcome(t, cli.mustCall("project_add", protocol.ProjectAddRequest{Dir: base, Scan: true, DryRun: true}))
	if !dry.DryRun || names(dry.Added) != "alpha,beta,gamma" || len(dry.Existing) != 0 || dry.Machine != "host-"+n.d.NodeID() {
		t.Fatalf("dry run: %+v", dry)
	}
	sk := map[string]string{}
	for _, s := range dry.Skipped {
		sk[s.Path] = s.Reason
	}
	if sk[filepath.Join(base, "alpha-task")] != "linked worktree of "+filepath.Join(base, "alpha") || sk[filepath.Join(base, "node_modules")] == "" || len(sk) != 2 {
		t.Fatalf("skipped: %v", sk)
	}
	if ps := listProjects(t, cli, ""); len(ps) != 0 {
		t.Fatalf("a dry run registered projects: %+v", ps)
	}

	done := addOutcome(t, cli.mustCall("project_add", protocol.ProjectAddRequest{Dir: base, Scan: true}))
	if names(done.Added) != "alpha,beta,gamma" || len(done.Existing) != 0 {
		t.Fatalf("scan: %+v", done)
	}
	if done.Added[0].Remote != "https://example.com/o/alpha.git" {
		t.Fatalf("remote kept credentials: %q", done.Added[0].Remote)
	}
	ps := listProjects(t, cli, "")
	if len(ps) != 3 || ps[0].Source != protocol.SourceRegistered || ps[0].AddedBy != "cli" || ps[0].Path != filepath.Join(base, "alpha") {
		t.Fatalf("projects: %+v", ps)
	}
	if lines := g.logs.lines("event=project.added", "host=host-"+n.d.NodeID(), "harness=cli"); len(lines) != 3 {
		t.Fatalf("log lines: %q", g.logs.lines())
	}

	again := addOutcome(t, cli.mustCall("project_add", protocol.ProjectAddRequest{Dir: base, Scan: true}))
	if len(again.Added) != 0 || names(again.Existing) != "alpha,beta,gamma" {
		t.Fatalf("rerun: %+v", again)
	}
	if len(g.logs.lines("event=project.added")) != 3 {
		t.Fatal("a rerun logged project.added again")
	}
}

func TestProjectAddByPathsResolvesAndSkips(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	base := codeDir(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Symlink(filepath.Join(base, "beta"), filepath.Join(home, "b")); err != nil {
		t.Fatal(err)
	}
	n := startNode(t, g, nodeOpts{})
	cli := cliClient(t, n)

	file := filepath.Join(base, "alpha", "f.txt")
	os.WriteFile(file, nil, 0o644)
	out := addOutcome(t, cli.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{
		filepath.Join(base, "alpha-task"), // a worktree: registers its main tree
		filepath.Join(base, "alpha"),      // the same project again in one call
		"~/b",                             // ~ is the daemon user's home; this is a symlink
		filepath.Join(base, "notes"),
		filepath.Join(base, "gone"),
		file,
		"relative/dir",
	}}))
	if names(out.Added) != "alpha,beta" || out.Added[0].Path != filepath.Join(base, "alpha") {
		t.Fatalf("added: %+v", out.Added)
	}
	reasons := map[string]string{}
	for _, s := range out.Skipped {
		reasons[s.Path] = s.Reason
	}
	want := map[string]string{
		filepath.Join(base, "notes"): "not a git working tree",
		filepath.Join(base, "gone"):  "the folder does not exist",
		file:                         "not a folder",
		"relative/dir":               "the path must be absolute or start with ~",
	}
	if len(reasons) != len(want) {
		t.Fatalf("skipped: %v", reasons)
	}
	for p, r := range want {
		if reasons[p] != r {
			t.Errorf("skip reason for %s = %q, want %q", p, reasons[p], r)
		}
	}
}

func TestProjectAddRefusesBadRequests(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cli := cliClient(t, n)
	for name, c := range map[string]struct {
		req  protocol.ProjectAddRequest
		want string
	}{
		"nothing":          {protocol.ProjectAddRequest{}, "give paths"},
		"dir without scan": {protocol.ProjectAddRequest{Dir: "/tmp"}, "scan=true"},
		"both":             {protocol.ProjectAddRequest{Dir: "/tmp", Scan: true, Paths: []string{"/tmp"}}, "not both"},
		"depth":            {protocol.ProjectAddRequest{Dir: "/tmp", Scan: true, Depth: 99}, "depth must be"},
		"relative dir":     {protocol.ProjectAddRequest{Dir: "code", Scan: true}, "absolute"},
		"missing dir":      {protocol.ProjectAddRequest{Dir: "/no/such/dir", Scan: true}, "does not exist"},
		"~user":            {protocol.ProjectAddRequest{Dir: "~root/code", Scan: true}, "not ~user"},
	} {
		if _, err := cli.call("project_add", c.req); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

// An agent registers folders on its own machine only, whatever it asks for.
func TestAgentRegistersOnItsOwnMachineOnly(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	base := codeDir(t)
	a := startNode(t, g, nodeOpts{cmdline: "pi"})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	agentA, sidA := helloPi(t, a, "na", filepath.Join(base, "alpha"), false)

	// Extra fields that name another machine are ignored.
	raw := agentA.mustCall("project_add", map[string]any{"paths": []string{filepath.Join(base, "beta")},
		"on": b.d.NodeID(), "node_id": b.d.NodeID(), "machine": "host-" + b.d.NodeID()})
	out := addOutcome(t, raw)
	if out.Machine != "host-"+a.d.NodeID() || names(out.Added) != "beta" {
		t.Fatalf("outcome: %+v", out)
	}
	ps := listProjects(t, agentA, "beta")
	if len(ps) != 1 || ps[0].NodeID != a.d.NodeID() || ps[0].Hostname != "host-"+a.d.NodeID() ||
		ps[0].AddedBy != "pi "+sidA || ps[0].Source != protocol.SourceRegistered {
		t.Fatalf("projects: %+v", ps)
	}
	if lines := g.logs.lines("event=project.added", "node="+a.d.NodeID(), "session="+sidA, "harness=pi", "name=beta"); len(lines) != 1 {
		t.Fatalf("log: %q", g.logs.lines())
	}
	// B has the same folder name registered by its own agent: a separate row.
	agentB, _ := helloPi(t, b, "nb", "/work/b", false)
	agentB.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{filepath.Join(base, "beta")}})
	if ps := listProjects(t, agentB, "beta"); len(ps) != 2 {
		t.Fatalf("want one row per machine: %+v", ps)
	}
	// Both of them see the project list.
	if ps := listProjects(t, agentA, ""); len(ps) < 2 {
		t.Fatalf("agent list: %+v", ps)
	}
}

func TestAgentCannotRemoveProjects(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	base := codeDir(t)
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	agent, _ := helloPi(t, n, "na", base, false)
	agent.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{filepath.Join(base, "beta")}})
	if _, err := agent.call("project_remove", protocol.ProjectRemove{Name: "beta"}); err == nil || !strings.Contains(err.Error(), "only available from the command line") {
		t.Fatalf("agent remove: %v", err)
	}
	if ps := listProjects(t, agent, "beta"); len(ps) != 1 {
		t.Fatalf("the project must stay: %+v", ps)
	}
}

func TestCLIRemoveByNameAndPath(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	base := codeDir(t)
	a := startNode(t, g, nodeOpts{})
	b := startNode(t, g, nodeOpts{})
	cliA, cliB := cliClient(t, a), cliClient(t, b)
	cliA.mustCall("project_add", protocol.ProjectAddRequest{Dir: base, Scan: true})
	cliB.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{filepath.Join(base, "beta")}})

	if _, err := cliA.call("project_remove", protocol.ProjectRemove{Name: "nope"}); err == nil || !strings.Contains(err.Error(), "no registered project") {
		t.Fatalf("missing: %v", err)
	}
	// Name: only this machine's project goes.
	cliA.mustCall("project_remove", protocol.ProjectRemove{Name: "beta"})
	ps := listProjects(t, cliA, "beta")
	if len(ps) != 1 || ps[0].NodeID != b.d.NodeID() {
		t.Fatalf("after remove by name: %+v", ps)
	}
	// Path, with ~ expanded.
	home := filepath.Dir(base)
	t.Setenv("HOME", home)
	cliA.mustCall("project_remove", protocol.ProjectRemove{Path: "~/" + filepath.Base(base) + "/group/gamma"})
	if ps := listProjects(t, cliA, "gamma"); len(ps) != 0 {
		t.Fatalf("after remove by path: %+v", ps)
	}
	// --on reaches another machine's entry (for a machine that is gone).
	cliA.mustCall("project_remove", protocol.ProjectRemove{Name: "beta", On: "host-" + b.d.NodeID()})
	if ps := listProjects(t, cliA, "beta"); len(ps) != 0 {
		t.Fatalf("after remove --on: %+v", ps)
	}
	if len(g.logs.lines("event=project.removed")) != 3 {
		t.Fatalf("log: %q", g.logs.lines())
	}
}

// A registered project that also has a session shows once, as both.
func TestRegisteredAndSeenProjectIsOneRow(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	base := codeDir(t)
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa})
	cli := cliClient(t, n)
	cli.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{filepath.Join(base, "alpha"), filepath.Join(base, "beta")}})
	s := fakeSession("na", "alice")
	s.Cwd = filepath.Join(base, "alpha-task") // a session in the worktree
	fa.setSessions(s)
	eventually(t, "session on the gateway", func() bool { return len(onlineAgents(t, cli)) == 1 })
	ps := listProjects(t, cli, "")
	if len(ps) != 2 || ps[0].Name != "alpha" || ps[0].Source != protocol.SourceBoth || ps[0].Sessions != 1 ||
		len(ps[0].Worktrees) != 1 || ps[1].Name != "beta" || ps[1].Source != protocol.SourceRegistered {
		t.Fatalf("projects: %+v", ps)
	}
}

func TestProjectAddAgainstOldGatewayIsAClearError(t *testing.T) {
	base := codeDir(t)
	url, _ := fakeOldGateway(t)
	cfg := Config{GatewayURL: url, NodeID: "n_old", Token: "t", Hostname: "newbox", StateDir: t.TempDir(),
		SocketPath: filepath.Join(shortDir(t), "d.sock"), BackoffMin: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	eventually(t, "connected to the old gateway", d.Connected)
	cli := dialLocal(t, cfg.SocketPath)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	start := time.Now()
	_, err = cli.call("project_add", protocol.ProjectAddRequest{Paths: []string{filepath.Join(base, "alpha")}})
	if err == nil || !strings.Contains(err.Error(), "gateway is too old for project registration") {
		t.Fatalf("add: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("the old gateway took too long to refuse")
	}
	if _, err := cli.call("project_remove", protocol.ProjectRemove{Name: "alpha"}); err == nil || !strings.Contains(err.Error(), "gateway is too old for project registration") {
		t.Fatalf("remove: %v", err)
	}
	// Nothing to check on the gateway when no folder passed the checks.
	if _, err := cli.call("project_add", protocol.ProjectAddRequest{Paths: []string{filepath.Join(base, "notes")}}); err != nil {
		t.Fatalf("no valid folder should not need the gateway: %v", err)
	}
}
