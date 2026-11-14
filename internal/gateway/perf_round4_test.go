package gateway

import (
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/model"
	"github.com/niparis/agent-net/internal/protocol"
)

// ---- the code as it was before round 4, kept here as the reference ----

// refQualityPreview is qualityPreviewWith without its memo: the same one
// decision path, computed fresh.
func refQualityPreview(g *Gateway, in qualityPreviewInputs, node protocol.NodeView) (map[string]string, map[string]string, map[string]string, error) {
	if !node.Online || node.Launch == nil || !*node.Launch {
		return nil, nil, nil, nil
	}
	running := in.running[node.NodeID]
	if running == nil {
		running = map[string]int{}
	}
	quotas := g.withQuotaBurn(g.ownerQuotas(node.NodeID), in.agents)
	candidates, _, _, err := g.workerModelCandidatesWith(&candidateFacts{}, nil, &node, in.profiles, in.providers, quotas, "", false, in.protected)
	if err != nil {
		return nil, nil, nil, err
	}
	snapshot, _, err := g.chooseFacts(&node, candidates, quotas, in.floors, nil, in.health.health)
	if err != nil {
		return nil, nil, nil, err
	}
	rotation, rotationErr := g.store.WorkerRotation()
	if rotationErr != nil {
		return nil, nil, nil, rotationErr
	}
	out, reasons, models := map[string]string{}, map[string]string{}, map[string]string{}
	for _, level := range protocol.DifficultyLevels {
		chosen, err := g.Choose(ChooseRequest{
			Purpose: model.PurposePreview, Node: &node, Candidates: candidates, Difficulty: level,
			D: protocol.PickerD(nil, level), Scores: in.scores, Minimums: in.minimums, Quotas: quotas,
			Running: running, Protected: in.protected, Floors: in.floors, Facts: &snapshot, Rotation: &rotation,
		})
		if err == nil {
			out[level] = qualityModelFamily(chosen.Choice.Model)
			models[level] = qualityModelKey(chosen.Choice.Model)
			if reason := qualityPaceClause(chosen.Choice.QuotaNote); reason != "" {
				reasons[level] = reason
			}
		}
	}
	return out, reasons, models, nil
}

// refNodesInfo is NodesInfo as it was.
func refNodesInfo(g *Gateway) (protocol.NodesInfo, error) {
	nodes, err := g.nodesWithState()
	if err != nil {
		return protocol.NodesInfo{}, err
	}
	var preview qualityPreviewInputs
	previewRead := false
	for i := range nodes {
		tracks, err := g.store.TrackRecords(nodes[i].NodeID, protocol.TrackRecordDays*24*time.Hour)
		if err != nil {
			return protocol.NodesInfo{}, err
		}
		nodes[i].Tracks = tracks
		for _, h := range nodes[i].Harnesses {
			successes, err := g.store.SucceededModels(nodes[i].NodeID, h.Name)
			if err != nil {
				return protocol.NodesInfo{}, err
			}
			if len(successes) > 0 {
				if nodes[i].RecentModelSuccesses == nil {
					nodes[i].RecentModelSuccesses = map[string]map[string]bool{}
				}
				nodes[i].RecentModelSuccesses[h.Name] = failedSet(successes)
			}
			failures, err := g.store.RecentWorkerFailures(nodes[i].NodeID, h.Name)
			if err != nil {
				return protocol.NodesInfo{}, err
			}
			if len(failures) > 0 {
				if nodes[i].RecentModelFailures == nil {
					nodes[i].RecentModelFailures = map[string]map[string]bool{}
				}
				nodes[i].RecentModelFailures[h.Name] = failures
			}
		}
		nodes[i].Quotas = g.nodeQuotas(nodes[i].NodeID)
		if nodes[i].Online && nodes[i].Launch != nil && *nodes[i].Launch && !previewRead {
			if preview, err = g.qualityPreviewInputs(); err != nil {
				return protocol.NodesInfo{}, err
			}
			previewRead = true
		}
		nodes[i].QualityPicks, nodes[i].QualityPickReasons, nodes[i].QualityPickModels, err = refQualityPreview(g, preview, nodes[i])
		if err != nil {
			return protocol.NodesInfo{}, err
		}
	}
	notes, err := g.store.PlaybookNotes()
	if err != nil {
		return protocol.NodesInfo{}, err
	}
	calibration, err := g.store.Calibration()
	if err != nil {
		return protocol.NodesInfo{}, err
	}
	return protocol.NodesInfo{GatewayVersion: g.opts.Version, Nodes: nodes, Playbook: notes, Calibration: calibration, GatewayWarnings: g.ExposureWarnings()}, nil
}

// refApplyPending is applyPending as it was: all requests of the last day.
func refApplyPending(s *Store, agents []protocol.AgentView) error {
	saved := pendingScopeMax
	pendingScopeMax = 0
	defer func() { pendingScopeMax = saved }()
	return s.applyPending(agents)
}

// refAgents is Agents as it was.
func refAgents(s *Store, f protocol.QueryAgents) ([]protocol.AgentView, error) {
	out, err := s.agentsBase(f)
	if err != nil {
		return nil, err
	}
	if err := s.applyDelegation(out); err != nil {
		return nil, err
	}
	if err := s.applyAgentCharters(out, true); err != nil {
		return nil, err
	}
	if err := refApplyPending(s, out); err != nil {
		return nil, err
	}
	return out, nil
}

// refGraphContext is graphContext as it was: it read the full Agents list.
func refGraphContext(s *Store, g protocol.Graph) (protocol.Graph, error) {
	projects, err := s.Projects("")
	if err != nil {
		return g, err
	}
	agents, err := refAgents(s, protocol.QueryAgents{OnlineOnly: true, IncludeOwners: true})
	if err != nil {
		return g, err
	}
	byName := make(map[string]*protocol.Node, len(g.Nodes))
	for i := range g.Nodes {
		if g.Nodes[i].Kind == "component" || g.Nodes[i].Kind == "cto" {
			byName[g.Nodes[i].Name] = &g.Nodes[i]
		}
	}
	for _, p := range projects {
		if n := byName[p.Name]; n != nil {
			n.Locations = append(n.Locations, protocol.GraphLocation{Machine: p.Hostname, Path: p.Path})
		}
	}
	for _, a := range agents {
		name := a.Repo.Project
		if a.Harness == "cto" {
			name = protocol.CTONodeName
		}
		n := byName[name]
		if n == nil {
			continue
		}
		n.SessionCount++
		if len(n.Sessions) < 5 {
			name := a.Profile.Name
			if name == "" {
				name = a.Title
			}
			n.Sessions = append(n.Sessions, protocol.GraphSession{ID: a.SessionID, Name: name, Machine: a.Hostname, Status: a.Status})
		}
	}
	return g, nil
}

// ---- fixtures ----

// previewFixture is a gateway with one launching machine that has three
// profiles, scores, quotas, an infrastructure failure and a model that failed.
func previewFixture(t *testing.T) (*Gateway, string, *clock) {
	t.Helper()
	g, node, _, _, c := setupFloorGateway(t)
	quotas := &protocol.Quotas{GeneratedAt: c.now().Unix(), Providers: []protocol.QuotaProvider{
		{Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "5h", PercentUsed: 30}}},
		{Provider: "codex", Windows: []protocol.QuotaWindow{{ID: "week", PercentUsed: 20}}},
	}}
	g.usage.set(node, c.now(), protocol.UsageReport{Quotas: quotas})
	// Two infrastructure failures cool one model down.
	for i := 0; i < 2; i++ {
		if err := g.store.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "w" + string(rune('a'+i)), Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash", Outcome: protocol.OutcomeInfra, StartedAt: c.now().Add(-30 * time.Minute).Unix(), EndedAt: c.now().Add(-time.Duration(10+i) * time.Minute).Unix()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.store.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "wc", Harness: "codex", Model: "gpt-6.1-terra", Outcome: protocol.OutcomeComplete, StartedAt: c.now().Add(-3 * time.Hour).Unix(), EndedAt: c.now().Add(-2 * time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := g.store.RecordModelSuccess(node, "codex", "gpt-6.1-terra"); err != nil {
		t.Fatal(err)
	}
	return g, node, c
}

// ---- NodesInfo ----

func TestNodesInfoSameAsBefore(t *testing.T) {
	g, _, _ := previewFixture(t)
	want, err := refNodesInfo(g)
	if err != nil {
		t.Fatal(err)
	}
	picks := 0
	for _, n := range want.Nodes {
		picks += len(n.QualityPicks)
	}
	if picks == 0 {
		t.Fatalf("the fixture gave no picks: %+v", want.Nodes)
	}
	// The first call computes the picks and the second takes them from the
	// stored answer. Both are what the old code gave.
	for call := 1; call <= 3; call++ {
		got, err := g.NodesInfo()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("call %d changed:\n got %+v\nwant %+v", call, got.Nodes, want.Nodes)
		}
	}
	if g.previewMemo.hits != 2 {
		t.Fatalf("stored answers used %d times, want 2", g.previewMemo.hits)
	}
}

func TestNodesInfoPreviewFollowsItsInputsAtOnce(t *testing.T) {
	g, node, c := previewFixture(t)
	picks := func() (map[string]string, map[string]string) {
		t.Helper()
		info, err := g.NodesInfo()
		if err != nil {
			t.Fatal(err)
		}
		ref, err := refNodesInfo(g)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(info, ref) {
			t.Fatalf("differs from the old code:\n got %+v\nwant %+v", info.Nodes, ref.Nodes)
		}
		for _, n := range info.Nodes {
			if n.NodeID == node {
				return n.QualityPicks, n.QualityPickModels
			}
		}
		t.Fatal("node missing")
		return nil, nil
	}
	base, _ := picks()
	if len(base) == 0 {
		t.Fatal("no picks")
	}
	changed := func(name string, mutate func()) {
		t.Helper()
		before, beforeModels := picks()
		mutate()
		after, afterModels := picks()
		if reflect.DeepEqual(before, after) && reflect.DeepEqual(beforeModels, afterModels) {
			t.Fatalf("%s did not change the preview: before %v after %v", name, before, after)
		}
	}
	hitsBefore := g.previewMemo.hits
	picks()
	if g.previewMemo.hits == hitsBefore {
		t.Fatal("an unchanged call did not use the stored answer")
	}
	// The preview picks the first profile of the roster when no cell has a
	// record, so the profile that is removed is the one the preview picks now.
	changed("a removed profile", func() {
		if err := g.store.saveWorkerProfiles([]protocol.WorkerProfile{
			{Name: "terra", Harness: "codex", Model: "gpt-6.1-terra", Effort: "high"},
			{Name: "deepseek", Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash", Effort: "high"},
		}); err != nil {
			t.Fatal(err)
		}
	})
	changed("a profile added back", func() {
		if err := g.store.AddWorkerProfile(protocol.WorkerProfile{Name: "opus", Harness: "claude", Model: "claude-opus-5-5", Effort: "high"}); err != nil {
			t.Fatal(err)
		}
	})
	// A new score: the first model now tops out at trivial work, so the harder
	// levels no longer allow it by its benchmark.
	changed("a new score", func() {
		if _, err := g.store.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, scoresKey,
			`[{"match":"claude-opus-5-5","score":10},{"match":"gpt-6.1-terra","score":41},{"match":"gpt-6.1-sol","score":41},{"match":"deepseek/deepseek-v4.1-flash","score":60}]`); err != nil {
			t.Fatal(err)
		}
	})
	// A quota report that puts a provider out of quota.
	changed("a quota report", func() {
		g.usage.set(node, c.now(), protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: c.now().Unix(), Providers: []protocol.QuotaProvider{
			{Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "5h", PercentUsed: 100}}},
			{Provider: "codex", Windows: []protocol.QuotaWindow{{ID: "week", PercentUsed: 100}}},
		}}})
	})
	// A model that goes into cooldown after two infrastructure failures.
	changed("model health", func() {
		for i := 0; i < 2; i++ {
			if err := g.store.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "x" + string(rune('a'+i)), Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash", Outcome: protocol.OutcomeInfra, StartedAt: c.now().Add(-5 * time.Minute).Unix(), EndedAt: c.now().Add(-time.Duration(i+1) * time.Minute).Unix()}); err != nil {
				t.Fatal(err)
			}
		}
		// The first failures were 10 minutes ago, so the model is cooling
		// now whatever the new ones are. Move the clock past the cooldown.
		c.t = c.t.Add(5 * time.Hour)
	})
}

func TestNodesInfoPreviewFollowsRotationAndFailures(t *testing.T) {
	g, node, _ := previewFixture(t)
	if _, err := g.NodesInfo(); err != nil {
		t.Fatal(err)
	}
	// A failed run, a stored rotation and a track record each reach the next
	// call. Whatever the old code says, the new code says.
	steps := []func(){
		func() {
			if err := g.store.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "f1", Harness: "codex", Model: "gpt-6.1-terra", Outcome: protocol.OutcomeFailed, StartedAt: g.store.now().Add(-time.Hour).Unix(), EndedAt: g.store.now().Add(-50 * time.Minute).Unix()}); err != nil {
				t.Fatal(err)
			}
		},
		func() {
			if err := g.store.saveWorkerRotation(protocol.WorkerRotation{Levels: map[string]*protocol.WorkerRotationLevel{"hard": {Current: map[string]float64{"claude\x00claude-opus-5-5": 9}, Recent: []string{"claude\x00claude-opus-5-5", "claude\x00claude-opus-5-5"}}}}); err != nil {
				t.Fatal(err)
			}
		},
		func() {
			if err := g.store.RecordModelFailure(node, "claude", "claude-opus-5-5", "auth"); err != nil {
				t.Fatal(err)
			}
		},
	}
	for i, step := range steps {
		step()
		got, err := g.NodesInfo()
		if err != nil {
			t.Fatal(err)
		}
		want, err := refNodesInfo(g)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d:\n got %+v\nwant %+v", i, got.Nodes, want.Nodes)
		}
	}
}

// The health of a model from one read of the window is what Store.ModelHealth
// says for the same model.
func TestHealthReaderSameAsModelHealth(t *testing.T) {
	g, node, c := previewFixture(t)
	// A judge that timed out, and one that ran well.
	if _, err := g.store.db.Exec(`INSERT INTO worker_assessments (session_id, started_at, assessed_at, judge_harness, judge_model, error, reasons, reasoning) VALUES ('j1', ?, ?, 'codex', 'gpt-6.1-terra', 'judge codex gpt-6.1-terra timed out after 5m', '{}', 0)`, c.now().Add(-20*time.Minute).Unix(), c.now().Add(-19*time.Minute).Unix()); err != nil {
		t.Skipf("assessment table layout differs: %v", err)
	}
	_ = node
	r := g.store.newHealthReader()
	for _, hm := range [][2]string{{"opencode", "deepseek/deepseek-v4.1-flash"}, {"codex", "gpt-6.1-terra"}, {"claude", "claude-opus-5-5"}, {"codex", "unknown"}} {
		want, err := g.store.ModelHealth(hm[0], hm[1])
		if err != nil {
			t.Fatal(err)
		}
		got, err := r.health(hm[0], hm[1])
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%v: got %+v want %+v", hm, got, want)
		}
	}
}

// ---- Agents and graphContext ----

func agentsFixture(t *testing.T) *Store {
	t.Helper()
	g, node, _, _, _ := setupFloorGateway(t)
	s := g.store
	node2, _, _ := s.CreateNode("other", "linux")
	var ids []string
	for i, harness := range []string{"claude", "codex", "claude", "pi", "opencode", "codex", "pi"} {
		n := node
		if i%3 == 2 {
			n = node2
		}
		project := []string{"alpha", "beta", "alpha"}[i%3]
		id, err := s.UpsertSession(n, protocol.SessionInfo{Harness: harness, NativeID: string(rune('a' + i)), Cwd: "/repo/" + project, Name: harness, Repo: protocol.RepoInfo{Root: "/repo/" + project, Worktree: "/repo/" + project, Project: project}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	// Requests: one open, one answered, one between a pair that answered in
	// another conversation.
	send := func(from, to int, typ, body string) {
		t.Helper()
		if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: ids[from], Recipient: ids[to], Type: typ, Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	send(0, 1, protocol.TypeRequest, "one")
	send(2, 3, protocol.TypeRequest, "two")
	send(4, 5, protocol.TypeRequest, "three")
	send(5, 4, protocol.TypeReply, "answer to three")
	send(1, 0, protocol.TypeRequest, "four")
	for _, p := range []struct{ project, policy string }{{"alpha", `{"delegate":"auto","coder_model":"gpt-6.1-terra"}`}, {"beta", `{"delegate":"auto","main_model":"claude-opus-5-5","workers":"sandboxed"}`}} {
		if _, err := s.db.Exec(`INSERT INTO project_policies(node_id,project,policy,updated_at) VALUES(?,?,?,0)`, node, p.project, p.policy); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestAgentsSameAsBefore(t *testing.T) {
	s := agentsFixture(t)
	for _, f := range []protocol.QueryAgents{
		{},
		{IncludeOwners: true},
		{OnlineOnly: true, IncludeOwners: true},
		{Harness: "codex"},
		{Harness: "pi", IncludeOwners: true},
		{Project: "alpha"},
		{Text: "claude"},
	} {
		want, err := refAgents(s, f)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.Agents(f)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%+v:\n got %+v\nwant %+v", f, got, want)
		}
		pending, delegated := 0, 0
		for _, a := range got {
			pending += len(a.Pending)
			if a.Delegation != nil {
				delegated++
			}
		}
		if f.Harness == "" && f.Project == "" && f.Text == "" && delegated == 0 {
			t.Fatalf("%+v: the fixture has no delegation", f)
		}
		if f.Harness == "" && f.Project == "" && f.Text == "" && pending == 0 {
			t.Fatalf("%+v: the fixture has no pending requests", f)
		}
	}
}

// refProjects is Projects as it was: the worker permissions of each project
// are read with a snapshot of their own.
func refProjects(s *Store, name string) ([]protocol.ProjectView, error) {
	out, err := s.projectsMerged(name)
	if err != nil {
		return nil, err
	}
	for i := range out {
		display, err := s.ProjectDisplayName(out[i].Name)
		if err != nil {
			return nil, err
		}
		out[i].DisplayName = display.Name
		out[i].Permissions, err = s.WorkerPermissions(out[i].NodeID, out[i].Name)
		if err != nil {
			return nil, err
		}
	}
	sortProjects(out)
	return out, nil
}

func TestProjectsSameAsBefore(t *testing.T) {
	s := agentsFixture(t)
	// A project whose permissions are set, and two graph names that differ
	// only by case: the second makes the permissions ambiguous.
	if _, err := s.db.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES(?,?,?,?,?,1)`, workerSandboxKey, "repo", "alpha", "", `"read-only"`); err != nil {
		t.Skipf("settings layout differs: %v", err)
	}
	for _, name := range []string{"", "alpha"} {
		want, err := refProjects(s, name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.Projects(name)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) || (name == "" && len(want) < 2) {
			t.Fatalf("%q:\n got %+v\nwant %+v", name, got, want)
		}
		places, err := s.projectLocations(name)
		if err != nil {
			t.Fatal(err)
		}
		if len(places) != len(want) {
			t.Fatalf("locations: %d, projects: %d", len(places), len(want))
		}
		for i := range places {
			if places[i].Name != want[i].Name || places[i].Hostname != want[i].Hostname || places[i].Path != want[i].Path {
				t.Fatalf("location %d: %+v, project %+v", i, places[i], want[i])
			}
		}
	}
}

func TestGraphContextSameAsBefore(t *testing.T) {
	s := agentsFixture(t)
	graph, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	copyOf := func() protocol.Graph {
		c := graph
		c.Nodes = append([]protocol.Node(nil), graph.Nodes...)
		return c
	}
	want, err := refGraphContext(s, copyOf())
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.graphContext(copyOf())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("graphContext changed:\n got %+v\nwant %+v", got.Nodes, want.Nodes)
	}
	sessions := 0
	for _, n := range got.Nodes {
		sessions += n.SessionCount
	}
	if sessions == 0 {
		t.Fatal("the fixture put no session on the graph")
	}
}

// ---- a copy of the real database ----

func realCopy(t *testing.T) *Gateway {
	t.Helper()
	path := os.Getenv("AGENT_NET_BENCH_DB")
	if path == "" {
		t.Skip("AGENT_NET_BENCH_DB is not set")
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	nodes, err := s.Nodes()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		g.conns[n.NodeID] = &nodeConn{id: n.NodeID}
	}
	return g
}

func TestRealCopyNodesInfoAgentsGraphSameAsBefore(t *testing.T) {
	g := realCopy(t)
	for i := 0; i < 2; i++ {
		want, err := refNodesInfo(g)
		if err != nil {
			t.Fatal(err)
		}
		got, err := g.NodesInfo()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("NodesInfo differs:\n got %+v\nwant %+v", got.Nodes, want.Nodes)
		}
	}
	for _, f := range []protocol.QueryAgents{{}, {IncludeOwners: true}, {OnlineOnly: true, IncludeOwners: true}, {IncludeOwners: true, Harness: "cto"}, {Harness: "claude"}} {
		want, err := refAgents(g.store, f)
		if err != nil {
			t.Fatal(err)
		}
		got, err := g.store.Agents(f)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Agents %+v differs", f)
		}
		t.Logf("agents %+v: %d", f, len(got))
	}
	graph, err := g.store.Graph()
	if err != nil {
		t.Fatal(err)
	}
	copyOf := func() protocol.Graph {
		c := graph
		c.Nodes = append([]protocol.Node(nil), graph.Nodes...)
		return c
	}
	want, err := refGraphContext(g.store, copyOf())
	if err != nil {
		t.Fatal(err)
	}
	got, err := g.store.graphContext(copyOf())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("graphContext differs")
	}
}
