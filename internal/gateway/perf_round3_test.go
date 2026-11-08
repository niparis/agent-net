package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The report that freezes new skills' defaults only runs when a skill has no
// settings. Either way the settings after a session report are the same.
func TestSessionUpsertFreezesDefaultsOfNewSkills(t *testing.T) {
	g, n, id := toggleFixture(t)
	if missing, err := g.store.skillSettingsMissing(); err != nil || !missing {
		t.Fatalf("a skill without settings: missing=%v err=%v", missing, err)
	}
	upsert := func() {
		raw, _ := json.Marshal(protocol.SessionUpsert{Session: protocol.SessionInfo{Harness: "claude", NativeID: "s-1", Cwd: "/repo/one", Repo: protocol.RepoInfo{Root: "/repo/one", Worktree: "/repo/one", Project: "one"}}})
		if _, err := g.handle(context.Background(), &nodeConn{id: n}, "session.upsert", raw); err != nil {
			t.Fatal(err)
		}
		g.standingWG.Wait()
	}
	upsert()
	if missing, err := g.store.skillSettingsMissing(); err != nil || missing {
		t.Fatalf("after the report: missing=%v err=%v", missing, err)
	}
	var seen int
	if err := g.store.db.QueryRow(`SELECT COUNT(*) FROM tool_project_seen WHERE item=?`, id).Scan(&seen); err != nil || seen != 2 {
		t.Fatalf("seen projects %d, %v", seen, err)
	}
	// A project that appears later is not frozen as seen by a report that
	// has nothing to freeze: the standing choice decides about it.
	if _, err := g.store.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES(?,?,?,0,0)`, n, "/repo/three", "three"); err != nil {
		t.Fatal(err)
	}
	upsert()
	if err := g.store.db.QueryRow(`SELECT COUNT(*) FROM tool_project_seen WHERE item=? AND project='three'`, id).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if seen != 1 {
		t.Fatalf("the standing choice should have seen project three, seen=%d", seen)
	}
}

// worker.profile.list reads the nodes without the quality picks and track
// records. Its answer is what the full NodesInfo gave.
func TestWorkerProfileListSameAsNodesInfo(t *testing.T) {
	s, _ := newTestStore(t)
	var nodes []string
	for _, host := range []string{"one", "two", "three"} {
		node, _, _ := s.CreateNode(host, "linux")
		if err := s.SetNodeHello(node, host, "test"); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, node)
	}
	harnesses := [][]protocol.HarnessInfo{
		{{Name: "codex", SignedIn: "yes", Models: []string{"gpt-6.1-sol", "gpt-6-luna"}}},
		{{Name: "codex", SignedIn: "no", Models: []string{"gpt-6.1-sol"}}, {Name: "opencode", SignedIn: "yes", Models: []string{"openai/gpt-6-luna"}}},
		{{Name: "claude", SignedIn: "yes", Models: []string{"opus"}}},
	}
	for i, node := range nodes {
		if _, err := s.SetNodeHarnesses(node, harnesses[i]); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []protocol.WorkerProfile{
		{Name: "sol", Harness: "codex", Model: "gpt-6.1-sol"},
		{Name: "luna", Harness: "opencode", Model: "gpt-6-luna"},
		{Name: "missing", Harness: "codex", Model: "gpt-9"},
		{Name: "none", Harness: "pi", Model: "x"},
	} {
		if err := s.AddWorkerProfile(p); err != nil {
			t.Fatal(err)
		}
	}
	// Two machines are connected, one is not.
	g := &Gateway{store: s, conns: map[string]*nodeConn{nodes[0]: {id: nodes[0]}, nodes[1]: {id: nodes[1]}}}
	got, err := g.workerProfileAction(nodes[0], "worker.profile.list", protocol.WorkerProfileFrame{})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := s.WorkerProfiles()
	if err != nil {
		t.Fatal(err)
	}
	info, err := g.NodesInfo()
	if err != nil {
		t.Fatal(err)
	}
	want := withWorkerProfileAvailability(profiles, info.Nodes, nil)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("worker.profile.list changed:\n got %+v\nwant %+v", got, want)
	}
	if len(want) != 4 {
		t.Fatalf("profiles: %+v", want)
	}
}

// Asking Agents for one harness gives the sessions of that harness out of the
// full list, in the same order and with the same fields.
func TestAgentsHarnessFilterIsASubsetOfAll(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("host", "linux")
	for i, harness := range []string{"claude", "codex", "claude", "pi", "opencode", "codex"} {
		if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: harness, NativeID: fmt.Sprintf("n%d", i), Cwd: "/repo/a", Name: harness}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.Agents(protocol.QueryAgents{IncludeOwners: true})
	if err != nil || len(all) != 6 {
		t.Fatalf("all: %d %v", len(all), err)
	}
	for _, harness := range []string{"claude", "codex", "pi", "opencode", "cto", "owner"} {
		var want []protocol.AgentView
		for _, a := range all {
			if a.Harness == harness {
				want = append(want, a)
			}
		}
		got, err := s.Agents(protocol.QueryAgents{IncludeOwners: true, Harness: harness})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) || (len(want) > 0 && !reflect.DeepEqual(got, want)) {
			t.Fatalf("%s:\n got %+v\nwant %+v", harness, got, want)
		}
	}
}
