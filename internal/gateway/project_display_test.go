package gateway

import (
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestProjectDisplayPersistenceAndClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	n, _, err := s.CreateNode("host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	sid, err := s.UpsertSession(n, protocol.SessionInfo{Harness: "pi", NativeID: "s", Repo: protocol.RepoInfo{Root: "/src/checkout", Project: "agent-net"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetProjectDisplayName("missing", "Bad"); err == nil {
		t.Fatal("accepted unknown project")
	}
	if _, err = s.SetProjectDisplayName("agent-net", "Agent Network"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	name, err := s.ProjectDisplayName("agent-net")
	if err != nil || name.Name != "Agent Network" {
		t.Fatalf("name=%+v err=%v", name, err)
	}
	p, err := s.Projects("agent-net")
	if err != nil || len(p) != 1 || p[0].Name != "agent-net" || p[0].DisplayName != "Agent Network" {
		t.Fatalf("projects=%+v err=%v", p, err)
	}
	a, ok, err := s.Agent(sid)
	if err != nil || !ok || a.Repo.Project != "agent-net" || a.ProjectDisplayName != "Agent Network" {
		t.Fatalf("agent=%+v err=%v", a, err)
	}
	as, err := s.Agents(protocol.QueryAgents{})
	if err != nil || len(as) != 1 || as[0].ProjectDisplayName != "Agent Network" {
		t.Fatalf("agents=%+v err=%v", as, err)
	}
	if _, err = s.SetProjectDisplayName("agent-net", ""); err != nil {
		t.Fatal(err)
	}
	a, ok, err = s.Agent(sid)
	if err != nil || !ok || a.ProjectDisplayName != "" {
		t.Fatalf("clear agent=%+v err=%v", a, err)
	}
}
