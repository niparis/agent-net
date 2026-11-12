package gateway

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

// findFixture is the acceptance fixture of the tool redesign, cut to the
// fields these read tools use: three machines, twelve sessions, four workers.
func findFixture(t *testing.T, s *Store, c *clock) map[string]string {
	t.Helper()
	c.t = time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	nodes := map[string]string{}
	for _, name := range []string{"research", "build", "desk"} {
		id, _, err := s.CreateNode(name, "linux")
		if err != nil {
			t.Fatal(err)
		}
		nodes[name] = id
		if err := s.SetNodeHello(id, name, "v0.3.2"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetNodeHarnesses(id, []protocol.HarnessInfo{{Name: "claude", Version: "1.0"}}); err != nil {
			t.Fatal(err)
		}
	}
	type spec struct {
		id, machine, project, harness, model string
		offline                              bool
		worker                               *protocol.WorkerInfo
	}
	specs := []spec{
		{"s01", "research", "housing", "claude", "standard", false, nil},
		{"s02", "research", "misc", "opencode", "standard", false, nil},
		{"s03", "research", "misc", "claude", "terra", false, nil},
		{"s04", "research", "misc", "claude", "standard", true, nil},
		{"s05", "build", "housing", "claude", "standard", false, &protocol.WorkerInfo{ID: "w02", Owner: "s01", StartedAt: 100, DifficultyUsed: "hard"}},
		{"s06", "build", "misc", "opencode", "standard", false, nil},
		{"s07", "build", "misc", "claude", "terra", false, nil},
		{"s08", "build", "misc", "claude", "standard", false, nil},
		{"s09", "desk", "housing", "claude", "standard", false, nil},
		{"s10", "desk", "misc", "claude", "standard", false, nil},
		{"s11", "desk", "misc", "claude", "standard", false, nil},
		{"s12", "desk", "misc", "claude", "standard", false, nil},
	}
	ids := map[string]string{}
	for _, sp := range specs {
		if sp.worker != nil {
			w := *sp.worker
			w.Owner = ids[w.Owner]
			w.StartedAt = time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC).Unix()
			sp.worker = &w
		}
		repo := protocol.RepoInfo{Root: "/work/" + sp.id, Branch: "main", Project: sp.project,
			Remote: "https://example.com/" + sp.project + ".git"}
		id, err := s.UpsertSession(nodes[sp.machine], protocol.SessionInfo{
			Harness: sp.harness, NativeID: "native-" + sp.id, Cwd: repo.Root,
			Status: "idle", Model: sp.model, Repo: repo, Worker: sp.worker,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[sp.id] = id
		if _, err := s.RenameSession(id, sp.id); err != nil {
			t.Fatal(err)
		}
		c.t = time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
		if _, err := s.Announce(nodes[sp.machine], protocol.ProfileAnnounce{
			SessionID: id, Task: "work on " + sp.id, Project: sp.project,
			Capabilities: []string{"cap-" + sp.id}, Expertise: "expert " + sp.id,
		}); err != nil {
			t.Fatal(err)
		}
		c.t = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		if sp.offline {
			if _, err := s.db.Exec(`UPDATE sessions SET online = 0, last_seen = ? WHERE id = ?`, c.t.UnixNano(), id); err != nil {
				t.Fatal(err)
			}
		}
	}
	return ids
}

func catalogOutput(t *testing.T, name string) *jsonschema.Resolved {
	t.Helper()
	for _, tool := range toolcatalog.Tools {
		if tool.Name != name {
			continue
		}
		var schema jsonschema.Schema
		if err := json.Unmarshal(tool.OutputSchema, &schema); err != nil {
			t.Fatalf("%s output schema: %v", name, err)
		}
		res, err := schema.Resolve(&jsonschema.ResolveOptions{})
		if err != nil {
			t.Fatalf("%s output schema: %v", name, err)
		}
		return res
	}
	t.Fatalf("%s is not in the tool catalog", name)
	return nil
}

func checkOutput(t *testing.T, name string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatal(err)
	}
	if err := catalogOutput(t, name).Validate(instance); err != nil {
		t.Fatalf("%s output does not match its catalog schema: %v\n%s", name, err, raw)
	}
}

func TestFindSessionsFiltersAndMatchAny(t *testing.T) {
	s, c := newTestStore(t)
	findFixture(t, s, c)

	page, err := s.FindSessions(protocol.SessionsFindRequest{Machine: "research", Project: "housing", Online: "any"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Name != "s01" || page.Total != 1 {
		t.Fatalf("research+housing: %+v", page)
	}

	page, err = s.FindSessions(protocol.SessionsFindRequest{
		MatchAny: []protocol.SessionMatch{{Harness: "opencode"}, {Model: "terra"}}, Online: "any",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, row := range page.Items {
		got = append(got, row.Name)
	}
	sort.Strings(got)
	want := []string{"s02", "s03", "s06", "s07"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("match_any: got %v want %v", got, want)
	}

	if _, err := s.FindSessions(protocol.SessionsFindRequest{MatchAny: []protocol.SessionMatch{}}); err == nil {
		t.Fatal("empty match_any must be refused")
	}
}

func TestFindSessionsPagingAndCursorBinding(t *testing.T) {
	s, c := newTestStore(t)
	findFixture(t, s, c)

	first, err := s.FindSessions(protocol.SessionsFindRequest{Online: "any", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 5 || first.Total != 12 || first.NextCursor == "" || first.Note == "" {
		t.Fatalf("first page: %+v", first)
	}
	second, err := s.FindSessions(protocol.SessionsFindRequest{Online: "any", Limit: 5, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 5 || second.NextCursor == "" {
		t.Fatalf("second page: %+v", second)
	}
	last, err := s.FindSessions(protocol.SessionsFindRequest{Online: "any", Limit: 5, Cursor: second.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Items) != 2 || last.NextCursor != "" || last.Note != "" {
		t.Fatalf("last page: %+v", last)
	}
	seen := map[string]bool{}
	for _, page := range []protocol.SessionsPage{first, second, last} {
		for _, row := range page.Items {
			if seen[row.ID] {
				t.Fatalf("row %s appears twice", row.Name)
			}
			seen[row.ID] = true
		}
	}
	if len(seen) != 12 {
		t.Fatalf("saw %d sessions, want 12", len(seen))
	}

	// The same cursor with another filter is refused.
	if _, err := s.FindSessions(protocol.SessionsFindRequest{Online: "online", Limit: 5, Cursor: first.NextCursor}); err == nil ||
		!strings.Contains(err.Error(), "cursor") {
		t.Fatalf("reusing a cursor with other filters: %v", err)
	}
}

func TestFindSessionsFullDetail(t *testing.T) {
	s, c := newTestStore(t)
	ids := findFixture(t, s, c)

	concise, err := s.FindSessions(protocol.SessionsFindRequest{Text: "s01", Online: "any"})
	if err != nil || len(concise.Items) != 1 {
		t.Fatalf("concise: %+v %v", concise, err)
	}
	row := concise.Items[0]
	if row.Folder != "" || row.Branch != "" || row.Capabilities != nil {
		t.Fatalf("concise row has full fields: %+v", row)
	}
	if row.TaskStarted == nil {
		t.Fatal("concise row has no task_started")
	}

	full, err := s.FindSessions(protocol.SessionsFindRequest{Text: "s01", Online: "any", Detail: "full"})
	if err != nil || len(full.Items) != 1 {
		t.Fatalf("full: %+v %v", full, err)
	}
	row = full.Items[0]
	if row.Folder != "/work/s01" || row.Branch != "main" || row.Remote == "" ||
		row.Expertise != "expert s01" || len(row.Capabilities) != 1 || row.LastSeenAgo == "" {
		t.Fatalf("full row is missing optional fields: %+v", row)
	}
	checkOutput(t, "agent_sessions_find", full)

	one, err := s.GetSession(protocol.SessionGetRequest{Session: ids["s01"]})
	if err != nil {
		t.Fatal(err)
	}
	if one.Session.Folder != "/work/s01" || one.Session.TaskStarted == nil {
		t.Fatalf("session get: %+v", one.Session)
	}
	checkOutput(t, "agent_session_get", one)

	byName, err := s.GetSession(protocol.SessionGetRequest{Session: "s05"})
	if err != nil || byName.Session.Name != "s05" || byName.Session.Kind != "worker" || byName.Session.Worker == nil {
		t.Fatalf("session get by name: %+v %v", byName, err)
	}
	if byName.Session.Worker.Caller.ID != ids["s01"] || byName.Session.Worker.Difficulty != "hard" {
		t.Fatalf("worker facts: %+v", byName.Session.Worker)
	}
}

func TestSessionReadCarriesLegacyCharter(t *testing.T) {
	s, c := newTestStore(t)
	ids := findFixture(t, s, c)
	for attr, value := range map[string]string{"purpose": "Housing tools", "accepts": "bug fixes; new pages", "refuses": "payment changes", "feature_requests": "ask the owner"} {
		if err := s.SetCharter("housing", attr, value, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	legacy, ok, err := s.Agent(ids["s01"])
	if err != nil || !ok {
		t.Fatalf("legacy agent_get source: %+v %v", legacy, err)
	}
	one, err := s.GetSession(protocol.SessionGetRequest{Session: ids["s01"]})
	if err != nil {
		t.Fatal(err)
	}
	if one.Session.Charter == nil || strings.Join(one.Session.Charter.Accepts, "; ") != legacy.Charter.Accepts || strings.Join(one.Session.Charter.Refuses, "; ") != legacy.Charter.Refuses {
		t.Fatalf("session charter %+v, legacy %+v", one.Session.Charter, legacy.Charter)
	}
	checkOutput(t, "agent_session_get", one)
	full, err := s.FindSessions(protocol.SessionsFindRequest{Text: "s01", Online: "any", Detail: "full"})
	if err != nil || len(full.Items) != 1 || full.Items[0].Charter == nil || strings.Join(full.Items[0].Charter.Accepts, "; ") != legacy.Charter.Accepts {
		t.Fatalf("full session find charter: %+v %v", full, err)
	}
	checkOutput(t, "agent_sessions_find", full)
	concise, err := s.FindSessions(protocol.SessionsFindRequest{Text: "s01", Online: "any"})
	if err != nil || len(concise.Items) != 1 || concise.Items[0].Charter != nil {
		t.Fatalf("concise session row: %+v %v", concise, err)
	}
}

func TestAnnounceTaskStarted(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("research", "linux")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n", Cwd: "/work/a"})
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	c.t = first
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: id, Task: "first task"}); err != nil {
		t.Fatal(err)
	}
	a, _, err := s.Agent(id)
	if err != nil {
		t.Fatal(err)
	}
	if a.TaskStarted != first.Unix() {
		t.Fatalf("task started %d, want %d", a.TaskStarted, first.Unix())
	}

	// The same task again keeps the recorded start.
	c.t = first.Add(time.Hour)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: id, Task: "first task"}); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(id)
	if a.TaskStarted != first.Unix() {
		t.Fatalf("same task moved task_started to %d", a.TaskStarted)
	}

	// A new task records a new start.
	c.t = first.Add(2 * time.Hour)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: id, Task: "second task"}); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(id)
	if a.TaskStarted != c.t.Unix() {
		t.Fatalf("new task started %d, want %d", a.TaskStarted, c.t.Unix())
	}
}

func TestFindMachinesAndMachineGet(t *testing.T) {
	s, c := newTestStore(t)
	findFixture(t, s, c)
	nodes, err := s.Nodes()
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	for _, n := range nodes {
		if n.Hostname != "desk" {
			g.conns[n.NodeID] = &nodeConn{id: n.NodeID}
		}
	}

	all, err := g.FindMachines(protocol.MachinesFindRequest{Online: "any"})
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 3 || len(all.Items) != 3 {
		t.Fatalf("machines: %+v", all)
	}
	checkOutput(t, "agent_machines_find", all)
	online, err := g.FindMachines(protocol.MachinesFindRequest{Online: "online"})
	if err != nil || online.Total != 2 {
		t.Fatalf("online machines: %+v %v", online, err)
	}
	full, err := g.FindMachines(protocol.MachinesFindRequest{Online: "any", Detail: "full"})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range full.Items {
		if row.OS == "" || row.LastSeen == "" {
			t.Fatalf("full machine row is missing optional fields: %+v", row)
		}
	}
	checkOutput(t, "agent_machines_find", full)

	var research protocol.NodeView
	for _, n := range nodes {
		if n.Hostname == "research" {
			research = n
		}
	}
	if _, err := s.SetNodeHarnesses(research.NodeID, []protocol.HarnessInfo{
		{Name: "claude", Version: "1.0", Models: []string{"claude-a", "claude-b"}},
		{Name: "opencode", Version: "2.0", Models: []string{"opencode/x"}},
	}); err != nil {
		t.Fatal(err)
	}
	nc, _ := fakeModelNodeFor(t, map[string][]string{
		"claude":   {"claude-a", "claude-b"},
		"opencode": {"opencode/x"},
	})
	nc.id = research.NodeID
	g.mu.Lock()
	if g.conns == nil {
		g.conns = map[string]*nodeConn{}
	}
	g.conns[research.NodeID] = nc
	g.mu.Unlock()

	one, err := g.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: "research", Include: []string{"models"}, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if one.Models == nil || len(one.Models.Items) != 2 || one.Models.Total != 3 || one.Models.NextCursor == "" || one.Models.Note == "" {
		t.Fatalf("models page: %+v", one.Models)
	}
	checkOutput(t, "agent_machine_get", one)
	next, err := g.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: "research", Include: []string{"models"}, Limit: 2, Cursor: one.Models.NextCursor})
	if err != nil || next.Models == nil || len(next.Models.Items) != 1 || next.Models.NextCursor != "" {
		t.Fatalf("models second page: %+v %v", next, err)
	}
	seen := map[string]bool{}
	for _, row := range append(one.Models.Items, next.Models.Items...) {
		key := row.Harness + "/" + row.ID
		if seen[key] {
			t.Fatalf("model %s appears twice", key)
		}
		seen[key] = true
	}

	if _, err := g.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: "research", Include: []string{"models", "quotas"}}); err == nil ||
		!strings.Contains(err.Error(), "one") {
		t.Fatalf("two include categories: %v", err)
	}
}

func TestFindProjects(t *testing.T) {
	s, c := newTestStore(t)
	findFixture(t, s, c)

	page, err := s.FindProjects(protocol.ProjectsFindRequest{Name: "housing"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Items) != 3 {
		t.Fatalf("housing projects: %+v", page)
	}
	checkOutput(t, "agent_projects_find", page)
	for _, row := range page.Items {
		if row.DisplayName != "" || row.Source == "" || row.Folder == "" || row.SessionsTotal == 0 {
			t.Fatalf("project row: %+v", row)
		}
		if row.Remote != "" || row.DefaultBranch != "" {
			t.Fatalf("concise project row has full fields: %+v", row)
		}
	}

	one, err := s.FindProjects(protocol.ProjectsFindRequest{Name: "housing", Machine: "build", Detail: "full"})
	if err != nil || one.Total != 1 || one.Items[0].Machine != "build" || one.Items[0].Remote == "" {
		t.Fatalf("build housing: %+v %v", one, err)
	}

	text, err := s.FindProjects(protocol.ProjectsFindRequest{Text: "s09"})
	if err != nil || text.Total != 1 || text.Items[0].Machine != "desk" {
		t.Fatalf("text s09: %+v %v", text, err)
	}
}

// TestReadToolAnswerSize records the size of the new concise answers against
// the old list answers on the same twenty-session fixture.
func TestReadToolAnswerSize(t *testing.T) {
	s, c := newTestStore(t)
	c.t = time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	nodes := map[string]string{}
	for _, name := range []string{"research", "build", "desk"} {
		id, _, err := s.CreateNode(name, "linux")
		if err != nil {
			t.Fatal(err)
		}
		nodes[name] = id
		if err := s.SetNodeHello(id, name, "v0.3.2"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetNodeHarnesses(id, []protocol.HarnessInfo{{Name: "claude", Version: "1.0", Models: []string{"claude-a", "claude-b", "claude-c"}}}); err != nil {
			t.Fatal(err)
		}
	}
	names := []string{"research", "build", "desk"}
	for i := 0; i < 20; i++ {
		name := names[i%3]
		id, err := s.UpsertSession(nodes[name], protocol.SessionInfo{
			Harness: "claude", NativeID: "n" + string(rune('a'+i)), Cwd: "/work/p" + string(rune('a'+i)),
			Status: "idle", Model: "standard",
			Repo: protocol.RepoInfo{Root: "/work/p" + string(rune('a'+i)), Branch: "main",
				Project: "p" + string(rune('a'+i%4)), Remote: "https://example.com/repo.git"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Announce(nodes[name], protocol.ProfileAnnounce{SessionID: id,
			Task: "work on item " + string(rune('a'+i)), Project: "p" + string(rune('a'+i%4)),
			Capabilities: []string{"go", "tests"}}); err != nil {
			t.Fatal(err)
		}
	}

	find, err := s.FindSessions(protocol.SessionsFindRequest{})
	if err != nil {
		t.Fatal(err)
	}
	oldAgents, err := s.Agents(protocol.QueryAgents{OnlineOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	for _, n := range nodes {
		g.conns[n] = &nodeConn{id: n}
	}
	machines, err := g.FindMachines(protocol.MachinesFindRequest{})
	if err != nil {
		t.Fatal(err)
	}
	oldNodes, err := s.Nodes()
	if err != nil {
		t.Fatal(err)
	}

	findJSON, _ := json.Marshal(find)
	agentJSON, _ := json.Marshal(protocol.AgentsResult{Agents: oldAgents})
	machineJSON, _ := json.Marshal(machines)
	nodeJSON, _ := json.Marshal(protocol.NodesInfo{Nodes: oldNodes})
	t.Logf("20 sessions: agent_sessions_find %d bytes, agent_list %d bytes", len(findJSON), len(agentJSON))
	t.Logf("3 machines: agent_machines_find %d bytes, agent_nodes %d bytes", len(machineJSON), len(nodeJSON))
}
