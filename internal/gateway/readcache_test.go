package gateway

import (
	"reflect"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A cached read must show every write that follows it.
func TestReadCacheShowsWritesAtOnce(t *testing.T) {
	s, _ := newTestStore(t)
	n, _, _ := s.CreateNode("h1", "linux")
	graphHas := func(name string) bool {
		g, err := s.Graph()
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range g.Nodes {
			if node.Name == name {
				return true
			}
		}
		return false
	}
	agentNamed := func(name string) bool {
		as, err := s.Agents(protocol.QueryAgents{})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range as {
			if a.Profile.Name == name || a.Repo.Project == name {
				return true
			}
		}
		return false
	}
	// Read twice so the second read is the cached one.
	for i := 0; i < 2; i++ {
		if graphHas("alpha") || agentNamed("alpha") {
			t.Fatal("alpha exists before it was created")
		}
	}
	if _, err := s.UpsertSession(n, protocol.SessionInfo{Harness: "claude", NativeID: "a", Cwd: "/work/alpha",
		Repo: protocol.RepoInfo{Root: "/work/alpha", Worktree: "/work/alpha", Project: "alpha"}}); err != nil {
		t.Fatal(err)
	}
	if !agentNamed("alpha") {
		t.Fatal("the agent list did not show a new session")
	}
	if !graphHas("alpha") {
		t.Fatal("the graph did not show a new session's project")
	}
	// A charter write shows in the graph.
	for i := 0; i < 2; i++ {
		if err := s.SetCharter("alpha", "purpose", "first"+string(rune('a'+i)), "owner"); err != nil {
			t.Fatal(err)
		}
		g, _ := s.Graph()
		g, _ = s.Graph()
		got := ""
		for _, node := range g.Nodes {
			if node.Name == "alpha" {
				got = node.Charter.Purpose
			}
		}
		if want := "first" + string(rune('a'+i)); got != want {
			t.Fatalf("charter purpose %q, want %q", got, want)
		}
	}
	// A settings write shows in the snapshot.
	before, _ := s.SettingsSnapshot()
	before, _ = s.SettingsSnapshot()
	if _, err := s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "alpha", Value: settingValue(`"full"`)}}, "owner", "t"); err != nil {
		t.Fatal(err)
	}
	after, _ := s.SettingsSnapshot()
	if after.Revision == before.Revision || len(after.Values) <= len(before.Values) {
		t.Fatalf("the snapshot did not change: revision %d -> %d", before.Revision, after.Revision)
	}
}

// Callers may change what they get without changing the next read.
func TestReadCacheGivesEachCallerACopy(t *testing.T) {
	s, _ := newTestStore(t)
	n, _, _ := s.CreateNode("h1", "linux")
	if _, err := s.UpsertSession(n, protocol.SessionInfo{Harness: "claude", NativeID: "a", Cwd: "/work/alpha",
		Repo: protocol.RepoInfo{Root: "/work/alpha", Worktree: "/work/alpha", Project: "alpha"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("alpha", "purpose", "keep me", "owner"); err != nil {
		t.Fatal(err)
	}
	g1, _ := s.Graph()
	a1, _ := s.Agents(protocol.QueryAgents{})
	snap1, _ := s.SettingsSnapshot()
	want := struct {
		g protocol.Graph
		a []protocol.AgentView
	}{copyOf(g1), copyOf(a1)}
	// Scribble over everything the first caller got.
	for i := range g1.Nodes {
		g1.Nodes[i].Name = "scribble"
		g1.Nodes[i].Charter.Purpose = "scribble"
		g1.Nodes[i].Settings = map[string]string{"x": "y"}
	}
	g1.Edges = append(g1.Edges, protocol.Edge{From: "x", To: "y"})
	for i := range a1 {
		a1[i].Profile.Name = "scribble"
		a1[i].Pending = append(a1[i].Pending, protocol.PendingRequest{MessageID: "scribble"})
	}
	snap1.Values = append(snap1.Values[:0], snap1.Values...)
	snap1.Maturity["scribble"] = "x"
	g2, _ := s.Graph()
	a2, _ := s.Agents(protocol.QueryAgents{})
	snap2, _ := s.SettingsSnapshot()
	if !reflect.DeepEqual(g2, want.g) {
		t.Fatal("the graph changed with the first caller's copy")
	}
	if !reflect.DeepEqual(a2, want.a) {
		t.Fatal("the agent list changed with the first caller's copy")
	}
	if _, bad := snap2.Maturity["scribble"]; bad {
		t.Fatal("the settings snapshot changed with the first caller's copy")
	}
}

// A read that depends on the time is kept for one second at most.
func TestReadCacheForTimedReadsLastOneSecond(t *testing.T) {
	s, clk := newTestStore(t)
	n, _, _ := s.CreateNode("h1", "linux")
	id, err := s.UpsertSession(n, protocol.SessionInfo{Harness: "claude", NativeID: "a", Cwd: "/work/alpha",
		Repo: protocol.RepoInfo{Root: "/work/alpha", Worktree: "/work/alpha", Project: "alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	loads := 0
	read := func() {
		if _, err := s.readThrough("timed", true, func() (any, error) { loads++; return id, nil }); err != nil {
			t.Fatal(err)
		}
	}
	read()
	read()
	if loads != 1 {
		t.Fatalf("%d loads in the same second", loads)
	}
	clk.t = clk.t.Add(1100 * time.Millisecond)
	read()
	if loads != 2 {
		t.Fatalf("%d loads after the clock moved a second", loads)
	}
}

// A value read while a write ran is not kept.
func TestReadCacheDropsReadsThatSawAWrite(t *testing.T) {
	s, _ := newTestStore(t)
	loads := 0
	read := func(during func()) {
		if _, err := s.readThrough("racy", false, func() (any, error) {
			loads++
			if during != nil {
				during()
			}
			return loads, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	read(func() { s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('probe','1',0)`) })
	read(nil) // not kept the first time, so it loads again
	read(nil) // and now it is kept
	if loads != 2 {
		t.Fatalf("%d loads, want 2", loads)
	}
}
