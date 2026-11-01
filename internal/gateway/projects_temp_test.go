package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A session in a temporary folder is still a session, but it is not a
// project: test suites that start sessions in /tmp must not fill the project
// list.
func TestProjectsSkipSessionsInTempFolders(t *testing.T) {
	s, _ := newTestStore(t)
	s.tempFolders = nil // the system temporary folders
	n, _, _ := s.CreateNode("box", "linux")
	session, err := s.UpsertSession(n, protocol.SessionInfo{Harness: "claude", NativeID: "tmp",
		Cwd: "/tmp/tmpabc123", Repo: protocol.RepoInfo{Project: "tmpabc123"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSession(n, protocol.SessionInfo{Harness: "claude", NativeID: "work",
		Cwd: "/src/shop", Repo: protocol.RepoInfo{Root: "/src/shop", Project: "shop"}}); err != nil {
		t.Fatal(err)
	}

	ps, err := s.Projects("")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Name != "shop" {
		t.Fatalf("projects: %+v, want only shop", ps)
	}
	// The session itself stays, with its folder.
	if _, ok, err := s.Agent(session); err != nil || !ok {
		t.Fatalf("the session must stay: ok=%v err=%v", ok, err)
	}
}

// TMPDIR names one more temporary folder, wherever it is.
func TestProjectsSkipSessionsUnderTMPDIR(t *testing.T) {
	dir := "/custom-tmp-for-agent-net-test"
	t.Setenv("TMPDIR", dir)
	s, _ := newTestStore(t)
	s.tempFolders = nil // the system temporary folders
	n, _, _ := s.CreateNode("box", "linux")
	if _, err := s.UpsertSession(n, protocol.SessionInfo{Harness: "pi", NativeID: "x", Cwd: dir + "/x"}); err != nil {
		t.Fatal(err)
	}
	ps, err := s.Projects("")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 0 {
		t.Fatalf("a TMPDIR session became a project: %+v", ps)
	}
}

// A daemon that knows a folder is temporary marks the session, even when the
// gateway does not know that folder.
func TestProjectsSkipSessionsMarkedNoProject(t *testing.T) {
	s, _ := newTestStore(t)
	s.tempFolders = nil // the system temporary folders
	n, _, _ := s.CreateNode("box", "linux")
	if _, err := s.UpsertSession(n, protocol.SessionInfo{Harness: "opencode", NativeID: "x",
		Cwd: "/scratch/run-7", NoProject: true}); err != nil {
		t.Fatal(err)
	}
	ps, err := s.Projects("")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 0 {
		t.Fatalf("a no-project session became a project: %+v", ps)
	}
}

// A normal folder still becomes a project, as before.
func TestProjectsKeepSessionsInNormalFolders(t *testing.T) {
	s, _ := newTestStore(t)
	s.tempFolders = nil // the system temporary folders
	n, _, _ := s.CreateNode("box", "linux")
	if _, err := s.UpsertSession(n, protocol.SessionInfo{Harness: "claude", NativeID: "x", Cwd: "/home/u/app"}); err != nil {
		t.Fatal(err)
	}
	ps, err := s.Projects("")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Name != "app" || ps[0].Path != "/home/u/app" {
		t.Fatalf("projects: %+v", ps)
	}
}
