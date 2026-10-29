package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func onlineAgents(t *testing.T, lc *localClient) []protocol.AgentView {
	t.Helper()
	var r struct {
		Agents []protocol.AgentView `json:"agents"`
	}
	if err := json.Unmarshal(lc.mustCall("list", map[string]any{"online_only": true}), &r); err != nil {
		t.Fatal(err)
	}
	return r.Agents
}

// A session in a git repository reaches the gateway with its project, remote
// and branch, the gateway logs session.new with them, and the branch follows
// the repository when the session updates.
func TestSessionRepoInfoReachesGateway(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo = filepath.Join(repo, "shop-api")
	sub := filepath.Join(repo, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "remote", "add", "origin", "https://bob:s3cret@git.example.com/team/shop-api.git")

	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa})
	cli := cliClient(t, n)
	info := fakeSession("na", "alice")
	info.Cwd = sub
	fa.setSessions(info)
	eventually(t, "session on the gateway", func() bool { return len(onlineAgents(t, cli)) == 1 })

	want := protocol.RepoInfo{Root: repo, Worktree: repo, Project: "shop-api", Remote: "https://git.example.com/team/shop-api.git", Branch: "main"}
	a := onlineAgents(t, cli)[0]
	if a.Repo != want {
		t.Fatalf("repo info %+v, want %+v", a.Repo, want)
	}
	if strings.Contains(a.Repo.Remote, "s3cret") || strings.Contains(a.Repo.Remote, "bob") {
		t.Fatal("credentials reached the gateway")
	}

	lines := g.logs.lines("event=session.new")
	if len(lines) != 1 {
		t.Fatalf("session.new lines: %v", lines)
	}
	for _, w := range []string{"session=" + a.SessionID, "harness=fake", "name=alice", "project=shop-api", "git_root=" + repo,
		"remote=https://git.example.com/team/shop-api.git", "branch=main", "cwd=" + sub} {
		if !strings.Contains(lines[0], w) {
			t.Errorf("session.new lacks %q: %s", w, lines[0])
		}
	}
	if strings.Contains(strings.Join(g.logs.lines(), "\n"), "s3cret") {
		t.Fatal("credentials in the gateway log")
	}

	// The branch changes. The next update of the session (here: its status) shows it.
	gitIn(t, repo, "checkout", "-q", "-b", "feature/x")
	info.Status = "busy"
	fa.setSessions(info)
	eventually(t, "new branch on the gateway", func() bool {
		as := onlineAgents(t, cli)
		return len(as) == 1 && as[0].Repo.Branch == "feature/x" && as[0].Status == "busy"
	})
	if n := len(g.logs.lines("event=session.new")); n != 1 {
		t.Fatalf("an update logged session.new again (%d)", n)
	}

	// The same through `projects`.
	var pr struct {
		Projects []protocol.ProjectView `json:"projects"`
	}
	if err := json.Unmarshal(cli.mustCall("projects", map[string]any{"name": "SHOP-API"}), &pr); err != nil {
		t.Fatal(err)
	}
	if len(pr.Projects) != 1 || pr.Projects[0].Path != repo || pr.Projects[0].Hostname != "host-"+n.d.NodeID() ||
		strings.Join(pr.Projects[0].Harnesses, ",") != "fake" || pr.Projects[0].Remote != want.Remote {
		t.Fatalf("projects: %+v", pr.Projects)
	}
	if err := json.Unmarshal(cli.mustCall("projects", map[string]any{"name": "other"}), &pr); err != nil || len(pr.Projects) != 0 {
		t.Fatalf("name filter: %+v %v", pr.Projects, err)
	}
}

// A plugin session (Pi) gets repo info from the hello it sends.
func TestPluginSessionRepoInfo(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	dir = filepath.Join(dir, "plain-folder")
	os.MkdirAll(dir, 0o755)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	pi, id := helloPi(t, n, "nb", dir, true)
	var r struct {
		Agent protocol.AgentView `json:"agent"`
	}
	json.Unmarshal(pi.mustCall("get", map[string]any{"session_id": id}), &r)
	if r.Agent.Repo.Project != "plain-folder" || r.Agent.Repo.Root != "" || r.Agent.Repo.Branch != "" {
		t.Fatalf("repo: %+v", r.Agent.Repo)
	}
}

// The full message trail in the gateway log: one line per step, never the body.
func TestMessageTrailInGatewayLog(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	startNode(t, g, nodeOpts{adapter: fa})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "alice registered", func() bool { return fa.host.SessionID("fake", "na") != "" })
	aliceID := fa.host.SessionID("fake", "na")
	pi, bobID := helloPi(t, b, "nb", "/work/bob", true)

	m := sendResult(t, pi.mustCall("send", map[string]any{"to": aliceID, "body": "classified-payload"}))
	eventually(t, "delivered", func() bool { return messageState(t, pi, m.ID) == protocol.StateDelivered })

	for _, ev := range []string{"msg.stored", "msg.dispatched", "msg.delivered"} {
		ls := g.logs.lines("event="+ev, "msg="+m.ID)
		if len(ls) != 1 {
			t.Fatalf("%s: %v", ev, ls)
		}
		if !strings.HasPrefix(ls[0], "ts=") || !strings.Contains(ls[0], "conv="+m.ConversationID) {
			t.Errorf("%s line: %s", ev, ls[0])
		}
	}
	stored := g.logs.lines("event=msg.stored", "msg="+m.ID)[0]
	for _, w := range []string{"type=request", "from=" + bobID, "from_name=bob", "from_harness=pi",
		"from_host=host-" + b.d.NodeID(), "to=" + aliceID, "to_name=alice", "to_harness=fake", "bytes=18"} {
		if !strings.Contains(stored, w) {
			t.Errorf("stored line lacks %q: %s", w, stored)
		}
	}
	if all := strings.Join(g.logs.lines(), "\n"); strings.Contains(all, "classified-payload") {
		t.Fatal("the message body is in the gateway log")
	}
	// The events table has the same trail.
	evs, err := g.gw.Store().QueryEvents(gateway.EventFilter{Session: bobID, Kind: "msg"})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	if got := strings.Join(kinds, ","); got != "msg.stored,msg.dispatched,msg.delivered" {
		t.Fatalf("events of the message: %s", got)
	}
}

func TestHarnessInventoryReachesGateway(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var mu sync.Mutex
	current := []protocol.HarnessInfo{{Name: "claude", Path: "/u/claude", Version: "2.1.0 (Claude Code)"}}
	runs := 0
	detect := func(context.Context) []protocol.HarnessInfo {
		mu.Lock()
		defer mu.Unlock()
		runs++
		return append([]protocol.HarnessInfo(nil), current...)
	}
	n := startNode(t, g, nodeOpts{detect: detect, interval: 150 * time.Millisecond})
	cli := cliClient(t, n)
	nodes := func() []protocol.NodeView {
		var info protocol.NodesInfo
		if err := json.Unmarshal(cli.mustCall("nodes", nil), &info); err != nil {
			t.Fatal(err)
		}
		return info.Nodes
	}
	eventually(t, "inventory on the gateway", func() bool {
		ns := nodes()
		return len(ns) == 1 && len(ns[0].Harnesses) == 1 && ns[0].Harnesses[0].Name == "claude"
	})
	if ls := g.logs.lines("event=node.harnesses", "change=+claude"); len(ls) != 1 {
		t.Fatalf("first report log: %v", g.logs.lines("event=node.harnesses"))
	}

	// Later runs find a new harness and a new version: one change line each time.
	mu.Lock()
	current = []protocol.HarnessInfo{{Name: "claude", Path: "/u/claude", Version: "2.2.0 (Claude Code)"},
		{Name: "opencode", Path: "/u/opencode", Version: "v2", Models: []string{"openai/gpt-x"}}}
	mu.Unlock()
	eventually(t, "new inventory", func() bool {
		ns := nodes()
		return len(ns) == 1 && len(ns[0].Harnesses) == 2
	})
	ns := nodes()
	if ns[0].Harnesses[1].Name != "opencode" || ns[0].Harnesses[1].Models[0] != "openai/gpt-x" || ns[0].Harnesses[0].Version != "2.2.0 (Claude Code)" {
		t.Fatalf("stored: %+v", ns[0].Harnesses)
	}
	// Let it run a few more times with no change.
	mu.Lock()
	seen := runs
	mu.Unlock()
	eventually(t, "more runs", func() bool { mu.Lock(); defer mu.Unlock(); return runs >= seen+2 })
	if n := len(g.logs.lines("event=node.harnesses")); n != 2 {
		t.Fatalf("change lines: %d, want 2 (first report and the change): %v", n, g.logs.lines("event=node.harnesses"))
	}
	if ls := g.logs.lines("event=node.harnesses", "+opencode"); len(ls) != 1 || !strings.Contains(ls[0], "claude:2.1.0") {
		t.Fatalf("change line: %v", ls)
	}
}

// Detection can take seconds. A daemon that connects before it ends sends an
// empty hello and the inventory follows as an update.
func TestHarnessInventoryAfterSlowDetection(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	release := make(chan struct{})
	detect := func(ctx context.Context) []protocol.HarnessInfo {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return []protocol.HarnessInfo{{Name: "pi", Path: "/u/pi", Version: "1.4.0"}}
	}
	n := startNode(t, g, nodeOpts{detect: detect})
	cli := cliClient(t, n)
	var info protocol.NodesInfo
	json.Unmarshal(cli.mustCall("nodes", nil), &info)
	if len(info.Nodes) != 1 || info.Nodes[0].Harnesses != nil {
		t.Fatalf("before detection ends, the node has not reported: %+v", info.Nodes)
	}
	close(release)
	eventually(t, "inventory after the update", func() bool {
		var info protocol.NodesInfo
		json.Unmarshal(cli.mustCall("nodes", nil), &info)
		return len(info.Nodes) == 1 && len(info.Nodes[0].Harnesses) == 1
	})
}

func TestEmptyInventoryMeansNoneInstalled(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: func(context.Context) []protocol.HarnessInfo { return nil }})
	cli := cliClient(t, n)
	eventually(t, "empty inventory reported", func() bool {
		var info protocol.NodesInfo
		json.Unmarshal(cli.mustCall("nodes", nil), &info)
		return len(info.Nodes) == 1 && info.Nodes[0].Harnesses != nil && len(info.Nodes[0].Harnesses) == 0
	})
}

func TestDaemonDatabaseIsPrivate(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	dir := t.TempDir()
	check := func(when string) {
		t.Helper()
		for _, f := range []string{"daemon.db", "daemon.db-wal", "daemon.db-shm"} {
			st, err := os.Stat(filepath.Join(dir, f))
			if err != nil {
				t.Fatalf("%s: %s: %v", when, f, err)
			}
			if st.Mode().Perm() != 0o600 {
				t.Errorf("%s: %s has mode %o, want 600", when, f, st.Mode().Perm())
			}
		}
	}
	s, err := openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.markDelivered("m_1")
	check("new")
	s.close()
	for _, f := range []string{"daemon.db", "daemon.db-wal", "daemon.db-shm"} {
		p := filepath.Join(dir, f)
		if _, err := os.Stat(p); err != nil {
			os.WriteFile(p, nil, 0o644)
		}
		os.Chmod(p, 0o644)
	}
	s, err = openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	check("reopened")
}
