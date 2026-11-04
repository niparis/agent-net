package daemon

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// daemonSession returns the local session with this native ID.
func daemonSession(t *testing.T, n *node, harness, native string) *protocol.SessionInfo {
	t.Helper()
	for _, in := range n.d.Sessions() {
		if in.Harness == harness && in.NativeID == native {
			in := in
			return &in
		}
	}
	return nil
}

// A session in /tmp is a session, but its folder is not a project.
func TestSessionInTempFolderHasNoProject(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	adapter := &fakeAdapter{harness: "claude"}
	n := startNode(t, g, nodeOpts{adapter: adapter, cfg: func(c *Config) {
		c.TempFolders = []string{"/tmp"}
	}})

	adapter.setSessions(protocol.SessionInfo{NativeID: "tmp1", PID: 1, Cwd: "/tmp/tmpabc123", Status: "idle"})
	eventually(t, "the temp session is known", func() bool { return daemonSession(t, n, "claude", "tmp1") != nil })
	in := daemonSession(t, n, "claude", "tmp1")
	if !in.NoProject || in.Repo != (protocol.RepoInfo{}) {
		t.Fatalf("temp session: no_project=%v repo=%+v", in.NoProject, in.Repo)
	}
	eventually(t, "the temp session reached the gateway", func() bool {
		ps, err := g.gw.Store().Projects("")
		return err == nil && len(ps) == 0
	})
}

// $TMPDIR names one more temp folder, and the daemon follows it.
func TestSessionUnderTMPDIRHasNoProject(t *testing.T) {
	// A folder outside the usual temp folders, so only TMPDIR can explain
	// why the session has no project.
	dir, err := os.MkdirTemp("/var/tmp", "agent-net-tmpdir-")
	if err != nil {
		t.Skipf("no /var/tmp to hold a custom TMPDIR: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TMPDIR", dir)
	g := startGateway(t, t.TempDir(), "")
	adapter := &fakeAdapter{harness: "claude"}
	n := startNode(t, g, nodeOpts{adapter: adapter, cfg: func(c *Config) {
		c.TempFolders = nil // the system list, which reads TMPDIR
	}})

	adapter.setSessions(protocol.SessionInfo{NativeID: "tmp2", PID: 1, Cwd: dir + "/x", Status: "idle"})
	eventually(t, "the TMPDIR session is known", func() bool { return daemonSession(t, n, "claude", "tmp2") != nil })
	if in := daemonSession(t, n, "claude", "tmp2"); !in.NoProject {
		t.Fatalf("TMPDIR session: no_project=%v repo=%+v", in.NoProject, in.Repo)
	}
	eventually(t, "the TMPDIR session reached the gateway", func() bool {
		ps, err := g.gw.Store().Projects("")
		return err == nil && len(ps) == 0
	})
}

// A session in a normal folder still makes a project.
func TestSessionInNormalFolderHasProject(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	adapter := &fakeAdapter{harness: "claude"}
	n := startNode(t, g, nodeOpts{adapter: adapter})

	adapter.setSessions(protocol.SessionInfo{NativeID: "work1", PID: 1, Cwd: "/work/keep", Status: "idle"})
	eventually(t, "the session is known", func() bool { return daemonSession(t, n, "claude", "work1") != nil })
	if in := daemonSession(t, n, "claude", "work1"); in.NoProject || in.Repo.Project != "keep" {
		t.Fatalf("normal session: no_project=%v repo=%+v", in.NoProject, in.Repo)
	}
	eventually(t, "the project reached the gateway", func() bool {
		ps, err := g.gw.Store().Projects("keep")
		return err == nil && len(ps) == 1 && ps[0].Path == "/work/keep"
	})
}

// AGENT_NET_IGNORE=1 in a session's process environment hides the session.
func TestSessionWithAgentNetIgnoreIsNotRegistered(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	adapter := &fakeAdapter{harness: "claude"}
	n := startNode(t, g, nodeOpts{adapter: adapter})

	const ignoredPID = 4242
	orig := processEnviron
	processEnviron = func(pid int) []string {
		if pid == ignoredPID {
			return []string{"AGENT_NET_IGNORE=1"}
		}
		return orig(pid)
	}
	defer func() { processEnviron = orig }()

	adapter.setSessions(
		protocol.SessionInfo{NativeID: "hidden", PID: ignoredPID, Cwd: "/work/hidden", Status: "idle"},
		protocol.SessionInfo{NativeID: "shown", PID: 4243, Cwd: "/work/shown", Status: "idle"},
	)
	eventually(t, "the shown session is known", func() bool { return daemonSession(t, n, "claude", "shown") != nil })
	if in := daemonSession(t, n, "claude", "hidden"); in != nil {
		t.Fatalf("an ignored session was registered: %+v", in)
	}
	if got := len(n.d.Sessions()); got != 1 {
		t.Fatalf("%d sessions, want only the shown one", got)
	}
	eventually(t, "the ignored project is absent", func() bool {
		ps, err := g.gw.Store().Projects("")
		if err != nil {
			return false
		}
		for _, p := range ps {
			if p.Name == "hidden" {
				return false
			}
		}
		return true
	})
}

// A plugin session whose process has AGENT_NET_IGNORE=1 is not registered.
func TestPluginSessionWithAgentNetIgnoreIsNotRegistered(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})

	orig := processEnviron
	processEnviron = func(pid int) []string {
		return []string{"AGENT_NET_IGNORE=1"}
	}
	defer func() { processEnviron = orig }()

	lc := dialLocal(t, n.sock)
	res := lc.mustCall("hello", map[string]any{"client": "pi", "harness": "pi", "native_id": "hidden-plugin", "cwd": "/work/pi", "status": "idle"})
	var r struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatal(err)
	}
	if r.SessionID != "" {
		t.Fatalf("an ignored plugin got session %q", r.SessionID)
	}
	if got := len(n.d.Sessions()); got != 0 {
		t.Fatalf("%d sessions, want none", got)
	}
}

// The daemon only reads the flag from the environment of a session process.
func TestAgentNetIgnoreNeedsValueOne(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	adapter := &fakeAdapter{harness: "claude"}
	n := startNode(t, g, nodeOpts{adapter: adapter})

	const pid = 4343
	orig := processEnviron
	processEnviron = func(p int) []string {
		if p == pid {
			return []string{"AGENT_NET_IGNORE=0"}
		}
		return orig(p)
	}
	defer func() { processEnviron = orig }()

	adapter.setSessions(protocol.SessionInfo{NativeID: "zero", PID: pid, Cwd: "/work/zero", Status: "idle"})
	eventually(t, "the session is known", func() bool { return daemonSession(t, n, "claude", "zero") != nil })
}
