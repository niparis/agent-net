package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCleanWorktreeSessions(t *testing.T) {
	s, _ := newTestStore(t)
	n1, _, _ := s.CreateNode("h1", "linux")
	n2, _, _ := s.CreateNode("h2", "linux")
	dir := "/state/daemon/worktrees"
	mk := func(node, native, cwd string, online bool, worker string) string {
		in := protocol.SessionInfo{Harness: "opencode", NativeID: native, Cwd: cwd}
		if worker != "" {
			in.Worker = &protocol.WorkerInfo{ID: worker}
		}
		id, err := s.UpsertSession(node, in)
		if err != nil {
			t.Fatal(err)
		}
		if !online {
			s.SetSessionOffline(node, id)
		}
		return id
	}
	ghost := mk(n1, "g1", dir+"/w_1", false, "")
	ghostLive := mk(n1, "g2", dir+"/w_2", true, "")
	worker := mk(n1, "g3", dir+"/w_3", false, "w_3")
	user := mk(n1, "g4", "/home/u/proj", false, "")
	sibling := mk(n1, "g5", dir+"-other/w_1", false, "")
	other := mk(n2, "g6", dir+"/w_1", false, "")
	n, err := s.CleanWorktreeSessions(n1, dir)
	if err != nil || n != 1 {
		t.Fatalf("cleaned %d, %v; want 1", n, err)
	}
	for id, want := range map[string]bool{ghost: false, ghostLive: true, worker: true, user: true, sibling: true, other: true} {
		if node, _ := s.SessionNode(id); (node != "") != want {
			t.Errorf("session %s present=%v, want %v", id, node != "", want)
		}
	}
	// A bad folder deletes nothing.
	if n, _ := s.CleanWorktreeSessions(n1, "/"); n != 0 {
		t.Errorf("root folder cleaned %d", n)
	}
	if n, _ := s.CleanWorktreeSessions(n1, "relative"); n != 0 {
		t.Errorf("relative folder cleaned %d", n)
	}
}
