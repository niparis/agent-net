package gateway

import (
	"errors"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func shopSession(t *testing.T, s *Store, node, native string) string {
	t.Helper()
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: native, Name: "harness", Cwd: "/code/shop",
		Repo: protocol.RepoInfo{Root: "/code/shop", Project: "shop"}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// The owner renames a session. The name survives every later report and is
// shown everywhere an automatic name is. An empty name returns to the
// automatic project name (decision 0033).
func TestOwnerRenameSurvivesReportsAndClearsToAutomatic(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	id := shopSession(t, s, node, "n1")
	res, err := s.RenameSession(id, "  reviewer ")
	if err != nil || res.SessionID != id || res.Name != "reviewer" {
		t.Fatalf("rename: %+v %v", res, err)
	}
	if a := agentOf(t, s, id); a.Profile.Name != "reviewer" || a.Title != "harness" {
		t.Fatalf("after rename: %+v", a)
	}
	// Reports and another session in the project do not move it.
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n1", Name: "harness", Cwd: "/code/shop",
		Repo: protocol.RepoInfo{Root: "/code/shop", Project: "shop"}}); err != nil {
		t.Fatal(err)
	}
	if got := agentOf(t, s, id).Profile.Name; got != "reviewer" {
		t.Fatalf("a report replaced the name: %q", got)
	}
	// An empty name returns to the automatic name.
	res, err = s.RenameSession(id, "  ")
	if err != nil || res.Name != "shop" {
		t.Fatalf("clear: %+v %v", res, err)
	}
	if got := agentOf(t, s, id).Profile.Name; got != "shop" {
		t.Fatalf("after clear: %q", got)
	}
	// The name comes back when the owner sets it again, and another session
	// in the project then gets the free automatic name.
	if res, err = s.RenameSession(id, "reviewer"); err != nil || res.Name != "reviewer" {
		t.Fatalf("rename again: %+v %v", res, err)
	}
	other := shopSession(t, s, node, "n2")
	if got := agentOf(t, s, other).Profile.Name; got != "shop" {
		t.Fatalf("the renamed session holds its automatic name too: %q", got)
	}
	// It is in the listings like every other name.
	ag, err := s.Agents(protocol.QueryAgents{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, v := range ag {
		seen[v.Profile.Name] = true
	}
	if !seen["reviewer"] || !seen["shop"] {
		t.Fatalf("names %v", seen)
	}
}

// A rename may not take the name of another live session, because agent_send
// uses exact names. An offline session does not hold its name.
func TestOwnerRenameRefusesAnotherLiveSessionsName(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	a := shopSession(t, s, node, "n1")
	b := shopSession(t, s, node, "n2")
	if got := agentOf(t, s, b).Profile.Name; got != "shop 2" {
		t.Fatalf("fixture name %q", got)
	}
	_, err := s.RenameSession(b, "shop")
	var bad ErrSessionName
	if !errors.As(err, &bad) || !strings.Contains(err.Error(), "live session") || !strings.Contains(err.Error(), "shop") {
		t.Fatalf("rename to a live name: %v", err)
	}
	// Its own current name is fine.
	if _, err := s.RenameSession(b, "shop 2"); err != nil {
		t.Fatalf("own name: %v", err)
	}
	// So is a name no live session holds, even if an offline one did.
	c := shopSession(t, s, node, "n3")
	s.SetSessionOffline(node, c)
	if _, err := s.RenameSession(b, "shop 3"); err != nil {
		t.Fatalf("offline name: %v", err)
	}
	// A name that looks like a session ID would break agent_send.
	if _, err := s.RenameSession(a, "s_1234"); err == nil {
		t.Fatal("a session ID was accepted as a name")
	}
	if _, err := s.RenameSession(a, "two\nlines"); err == nil {
		t.Fatal("a control character was accepted")
	}
	if _, err := s.RenameSession("s_nope", "x"); err == nil {
		t.Fatal("unknown session accepted")
	}
}

// A session renamed away from its automatic name frees that name for another
// session. Clearing the rename must not take the name back from it.
func TestOwnerRenameClearTakesTheNextFreeNumber(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	a := shopSession(t, s, node, "n1")
	if _, err := s.RenameSession(a, "reviewer"); err != nil {
		t.Fatal(err)
	}
	b := shopSession(t, s, node, "n2")
	if got := agentOf(t, s, b).Profile.Name; got != "shop" {
		t.Fatalf("the freed name went to %q", got)
	}
	res, err := s.RenameSession(a, "")
	if err != nil || res.Name != "shop 2" {
		t.Fatalf("clear: %+v %v", res, err)
	}
	if got := agentOf(t, s, b).Profile.Name; got != "shop" {
		t.Fatalf("the clear took the name of the other session: %q", got)
	}
}
