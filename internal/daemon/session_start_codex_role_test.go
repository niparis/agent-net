package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func codexRoleFixture(t *testing.T) (*workerNode, *testGateway, *fakeShared) {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	bins := fakeBin(t, "codex")
	n := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = bins
		c.CodexHome = f.home
		c.CodexInterval = 20 * time.Millisecond
	})
	return n, g, f
}

func sharedThreadParams(f *fakeShared, id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.threads[id].Params
}

func requireCodexRolePolicy(t *testing.T, params map[string]any) map[string]any {
	t.Helper()
	if params["approvalPolicy"] != "never" || params["sandbox"] != "workspace-write" {
		t.Fatalf("role thread policy = %v", params)
	}
	cfg, _ := params["config"].(map[string]any)
	sandbox, _ := cfg["sandbox_workspace_write"].(map[string]any)
	if sandbox["network_access"] != true {
		t.Fatalf("role thread network = %v", params)
	}
	return cfg
}

// A chief and a merger need unattended turns on the shared app server. The
// model effort must survive the sandbox configuration.
func TestCodexRoleStartUsesUnattendedPolicy(t *testing.T) {
	n, _, f := codexRoleFixture(t)
	repo := newRepo(t, "shop")
	merger, err := n.d.mergerFolder(context.Background(), repo, "shop")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ role, dir string }{{"chief", t.TempDir()}, {"merger", merger}} {
		t.Run(tc.role, func(t *testing.T) {
			r, err := n.d.prepareOwnerCodex(context.Background(), protocol.StartedSession{ID: "start_" + tc.role, Type: tc.role, Harness: "codex", Path: tc.dir, Model: "gpt-fake:high"})
			if err != nil {
				t.Fatal(err)
			}
			params := sharedThreadParams(f, r.NativeID)
			cfg := requireCodexRolePolicy(t, params)
			if cfg["model_reasoning_effort"] != "high" {
				t.Fatalf("model effort lost: %v", cfg)
			}
			if tc.role == "merger" {
				shell, _ := cfg["shell_environment_policy"].(map[string]any)
				set, _ := shell["set"].(map[string]any)
				if set["GOCACHE"] != n.d.workerGoCacheDir() {
					t.Fatalf("merger environment = %v", cfg)
				}
				sandbox := cfg["sandbox_workspace_write"].(map[string]any)
				roots := anyStrings(sandbox["writable_roots"])
				for _, want := range []string{filepath.Join(repo, ".git", "objects"), filepath.Join(repo, ".git", "refs"), filepath.Join(repo, ".git", "logs"), filepath.Join(repo, ".git", "worktrees", "shop"), n.d.workerGoCacheDir(), n.d.workerSandboxGoTmpDir()} {
					found := false
					for _, root := range roots {
						found = found || root == want
					}
					if !found {
						t.Errorf("writable roots %v lack %s", roots, want)
					}
				}
			}
		})
	}
}

// The policy also applies when a saved chief or merger is woken by a message.
func TestCodexRoleWakeKeepsUnattendedPolicy(t *testing.T) {
	n, g, f := codexRoleFixture(t)
	for _, role := range []string{"chief", "merger"} {
		t.Run(role, func(t *testing.T) {
			dir := t.TempDir()
			id, err := g.gw.Store().UpsertSession(n.d.NodeID(), protocol.SessionInfo{Harness: "codex", NativeID: "sleeping-" + role, Cwd: dir})
			if err != nil {
				t.Fatal(err)
			}
			if role == "chief" {
				if err := g.gw.Store().SetChief(protocol.ChiefSetRequest{SessionID: id}, "owner"); err != nil {
					t.Fatal(err)
				}
			}
			a := protocol.AgentView{SessionID: id, Harness: "codex", NativeID: "sleeping-" + role, Cwd: dir}
			if role == "merger" {
				a.Type = "merger"
			}
			if err := n.d.wakeCodex(context.Background(), a); err != nil {
				t.Fatal(err)
			}
			cfg := requireCodexRolePolicy(t, sharedThreadParams(f, a.NativeID))
			if role == "merger" {
				shell, _ := cfg["shell_environment_policy"].(map[string]any)
				set, _ := shell["set"].(map[string]any)
				if set["GOCACHE"] != n.d.workerGoCacheDir() {
					t.Fatalf("woken merger environment = %v", cfg)
				}
			}
		})
	}
}

func TestCodexRoleManualResumeKeepsUnattendedPolicy(t *testing.T) {
	n, g, f := codexRoleFixture(t)
	dir := t.TempDir()
	id, err := g.gw.Store().UpsertSession(n.d.NodeID(), protocol.SessionInfo{Harness: "codex", NativeID: "saved-chief", Cwd: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetChief(protocol.ChiefSetRequest{SessionID: id}, "owner"); err != nil {
		t.Fatal(err)
	}
	a := protocol.AgentView{SessionID: id, NodeID: n.d.NodeID(), Harness: "codex", NativeID: "saved-chief", Cwd: dir}
	if _, err := n.d.resumeAgent(context.Background(), a, "", ""); err != nil {
		t.Fatal(err)
	}
	requireCodexRolePolicy(t, sharedThreadParams(f, a.NativeID))
}

func TestOrdinaryCodexStartKeepsOwnerPolicy(t *testing.T) {
	n, _, f := codexRoleFixture(t)
	r, err := n.d.prepareOwnerCodex(context.Background(), protocol.StartedSession{ID: "start_owner", Harness: "codex", Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	params := sharedThreadParams(f, r.NativeID)
	if params["approvalPolicy"] != nil || params["sandbox"] != nil || params["config"] != nil {
		t.Fatalf("ordinary thread got a role policy: %v", params)
	}
}
