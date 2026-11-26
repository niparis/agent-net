package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// policyFixture makes a node with an OpenCode model list and three sessions:
// two OpenCode sessions in project cogforge and one Pi session in project
// other.
func policyFixture(t *testing.T) (s *Store, node, oc1, oc2, pi string) {
	t.Helper()
	s, _ = newTestStore(t)
	node, _, err := s.CreateNode("host-a", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetNodeHarnesses(node, []protocol.HarnessInfo{
		{Name: "opencode", Models: []string{"deepseek/deepseek-v4.1-flash", "deepseek/deepseek-v4.1-pro", "sol/sol-6.1", "anthropic/claude-haiku-4-5"}},
		{Name: "pi"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE nodes SET seen_at = 1 WHERE id = ?`, node); err != nil {
		t.Fatal(err)
	}
	up := func(h, native, project string) string {
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: h, NativeID: native, Name: native, Cwd: "/w/" + native,
			Repo: protocol.RepoInfo{Project: project}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	return s, node, up("opencode", "a", "cogforge"), up("opencode", "b", "cogforge"), up("pi", "c", "other")
}

func TestWorkerPermissionsAreAProjectSetting(t *testing.T) {
	s, node, oc1, _, _ := policyFixture(t)
	_ = oc1
	level := func(project string) string {
		t.Helper()
		l, err := s.WorkerPermissions(node, project)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	if got := level("cogforge"); got != protocol.WorkersSandboxed {
		t.Fatalf("default level %q", got)
	}
	// The repo value applies on every machine; another project keeps the default.
	if err := s.SetNodeSetting("cogforge", protocol.SettingWorkerSandbox, protocol.WorkersSandboxed, "owner"); err != nil {
		t.Fatal(err)
	}
	if got := level("other"); got != protocol.WorkersSandboxed {
		t.Errorf("another project got %q", got)
	}
	if got, _ := s.WorkerPermissions("n_elsewhere", "cogforge"); got != protocol.WorkersSandboxed {
		t.Errorf("another machine got %q", got)
	}
	ps, err := s.Projects("cogforge")
	if err != nil || len(ps) == 0 {
		t.Fatalf("projects: %v %v", ps, err)
	}
	for _, p := range ps {
		if p.NodeID == node && p.Permissions != protocol.WorkersSandboxed {
			t.Errorf("project level: %+v", p)
		}
	}
	if err := s.SetNodeSetting("cogforge", protocol.SettingWorkerSandbox, "", "owner"); err != nil {
		t.Fatal(err)
	}
	if got := level("cogforge"); got != protocol.WorkersSandboxed {
		t.Errorf("after clearing: %q", got)
	}
}
