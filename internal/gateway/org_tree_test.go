package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// reportsTo is the parent the org tree stores for an Agent, or "".
func reportsTo(t *testing.T, s *Store, agent string) string {
	t.Helper()
	var parent string
	err := s.db.QueryRow(`SELECT COALESCE((SELECT target FROM graph_edges WHERE source=? AND kind='reports_to'),'')`, agent).Scan(&parent)
	if err != nil {
		t.Fatal(err)
	}
	return parent
}

// typedNode is a daemon connection that reported typed Graph support.
func typedNode(id string) *nodeConn {
	nc := &nodeConn{id: id}
	nc.graphRefs.Store(true)
	return nc
}

func reportsEdge(from, to string) protocol.Edge {
	return protocol.Edge{From: from, To: to, Kind: protocol.EdgeReportsTo}
}

func TestReportsToKeepsOneParent(t *testing.T) {
	s := teamStore(t)
	if err := s.SetEdge(reportsEdge("demo", "other"), "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEdge(reportsEdge("demo", "cto"), "owner"); err == nil {
		t.Fatal("a second parent was accepted")
	}
	if got := reportsTo(t, s, "demo"); got != "other" {
		t.Fatalf("demo reports to %q, want other", got)
	}
	if err := s.RemoveEdge(reportsEdge("demo", "other"), "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEdge(reportsEdge("demo", "cto"), "owner"); err != nil {
		t.Fatalf("a new parent after the old one went: %v", err)
	}
}

func TestReportsToRefusesACycle(t *testing.T) {
	s := teamStore(t)
	for _, e := range []protocol.Edge{reportsEdge("demo", "other"), reportsEdge("other", "cto")} {
		if err := s.SetEdge(e, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetEdge(reportsEdge("cto", "demo"), "owner"); err == nil {
		t.Fatal("a cycle was accepted")
	}
}

// The tree is stored now; the Graph reads take it in a later task. Until then
// a reports_to edge must not show as a graph edge or add an implicit node.
func TestReportsToStaysOutOfTheGraphReads(t *testing.T) {
	s := teamStore(t)
	if err := s.SetEdge(reportsEdge("cto", protocol.OrgRoot), "owner"); err != nil {
		t.Fatal(err)
	}
	for _, typed := range []bool{false, true} {
		var g protocol.Graph
		var err error
		if typed {
			g, err = s.GraphForRef(nil)
		} else {
			g, err = s.Graph()
		}
		if err != nil {
			t.Fatalf("typed=%v: %v", typed, err)
		}
		for _, e := range g.Edges {
			if e.Kind == protocol.EdgeReportsTo {
				t.Fatalf("typed=%v: graph shows %+v", typed, e)
			}
		}
		for _, n := range g.Nodes {
			if n.Name == protocol.OrgRoot {
				t.Fatalf("typed=%v: graph shows a node for the root: %+v", typed, n)
			}
		}
	}
}

func TestEdgeSetReturnsTheStoredEdge(t *testing.T) {
	s := teamStore(t)
	g := &Gateway{store: s}
	raw, _ := json.Marshal(map[string]string{"from": "demo", "to": protocol.OrgRoot, "kind": protocol.EdgeReportsTo})
	out, err := g.handle(context.Background(), typedNode("n"), "edge.set", raw)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	var got struct {
		OK   bool `json:"ok"`
		Edge struct {
			From protocol.GraphRef `json:"from"`
			To   protocol.GraphRef `json:"to"`
			Kind string            `json:"kind"`
			Rule string            `json:"rule"`
		} `json:"edge"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	if !got.OK || got.Edge.From != (protocol.GraphRef{Kind: "agent", ID: "demo"}) ||
		got.Edge.To != (protocol.GraphRef{Kind: "special", ID: protocol.OrgRoot}) ||
		got.Edge.Kind != protocol.EdgeReportsTo || got.Edge.Rule != "normal" {
		t.Fatalf("edge.set answered %s", b)
	}
}

// One Agent with three sessions, one of them its lead session: the homelab
// pattern. lead_set stores the session on the Agent and answers the row.
func TestLeadSetStoresTheAgentLeadSession(t *testing.T) {
	s := teamStore(t)
	_, node := graphSessions(t, s)
	var ids []string
	for _, native := range []string{"h1", "h2", "h3"} {
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: native, Repo: protocol.RepoInfo{Root: "/w/homelab", Project: "homelab"}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "owner", NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s}
	raw, _ := json.Marshal(protocol.LeadSetRequest{Owner: owner, Ref: protocol.GraphRef{Kind: "repo", ID: "homelab"},
		Lead: &protocol.GraphRef{Kind: "session", ID: ids[1]}})
	out, err := g.handle(context.Background(), typedNode(node), "lead.set", raw)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	var got protocol.LeadSetResult
	if err := json.Unmarshal(b, &got); err != nil || !got.OK || got.Agent != "homelab" || got.LeadSession != ids[1] {
		t.Fatalf("lead.set answered %s %v", b, err)
	}
	var stored string
	if err := s.db.QueryRow(`SELECT COALESCE(lead_session,'') FROM agents WHERE id='homelab'`).Scan(&stored); err != nil || stored != ids[1] {
		t.Fatalf("agents.lead_session=%q %v", stored, err)
	}
}

// teams.lead_session and repo_leads.lead_session are copies of the Agent's
// lead session: a new lead session moves every copy that names one.
func TestLeadSessionCopiesFollowTheAgent(t *testing.T) {
	s := teamStore(t)
	_, node := graphSessions(t, s)
	second, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "demo-2", Repo: protocol.RepoInfo{Root: "/w/demo", Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	var first string
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE native_id='n-demo'`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha"})
	for _, step := range []struct {
		ref     protocol.GraphRef
		session string
	}{{protocol.GraphRef{Kind: "team", ID: "alpha"}, first}, {protocol.GraphRef{Kind: "repo", ID: "demo"}, second}} {
		if err := s.SetLead(protocol.LeadSetRequest{Ref: step.ref, Lead: &protocol.GraphRef{Kind: "session", ID: step.session}}, "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	var team, repo, agent string
	if err := s.db.QueryRow(`SELECT (SELECT lead_session FROM teams WHERE id='alpha'),(SELECT lead_session FROM repo_leads WHERE repo_id='demo'),(SELECT lead_session FROM agents WHERE id='demo')`).Scan(&team, &repo, &agent); err != nil {
		t.Fatal(err)
	}
	if team != second || repo != second || agent != second {
		t.Fatalf("team=%q repo=%q agent=%q, want all %q", team, repo, agent, second)
	}
}

// mergerRig registers a merger the way merger_start does.
func mergerRig(t *testing.T) (*Store, string) {
	t.Helper()
	s := teamStore(t)
	var node, session string
	if err := s.db.QueryRow(`SELECT node_id,id FROM sessions WHERE native_id='n-demo'`).Scan(&node, &session); err != nil {
		t.Fatal(err)
	}
	merger, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "merge", Repo: protocol.RepoInfo{Root: "/w/demo", Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: node, SessionID: merger, Configure: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	return s, merger
}

func TestMergerStartGivesTheMergerItsOwnAgent(t *testing.T) {
	s, merger := mergerRig(t)
	var agent, role, startedBy string
	if err := s.db.QueryRow(`SELECT s.agent_id,a.role,s.started_by FROM sessions s JOIN agents a ON a.id=s.agent_id WHERE s.id=?`, merger).Scan(&agent, &role, &startedBy); err != nil {
		t.Fatal(err)
	}
	if agent != "demo merger" || role != "merger" || startedBy != protocol.StartedByAgentNet {
		t.Fatalf("merger session: agent=%q role=%q started_by=%q", agent, role, startedBy)
	}
	if got := reportsTo(t, s, "demo merger"); got != "demo" {
		t.Fatalf("merger reports to %q, want the repo's top agent demo", got)
	}
}

func TestMergerIsNeverALead(t *testing.T) {
	s, merger := mergerRig(t)
	a, ok, err := s.Agent(merger)
	if err != nil || !ok || a.RepoLead {
		t.Fatalf("merger counts as repo lead: %+v %v", a, err)
	}
	for _, lead := range []protocol.GraphRef{{Kind: "agent", ID: "demo merger"}, {Kind: "session", ID: merger}} {
		if err := s.SetLead(protocol.LeadSetRequest{Ref: protocol.GraphRef{Kind: "repo", ID: "demo"}, Lead: &lead}, "owner", false); err == nil {
			t.Fatalf("lead_set accepted the merger as %s", lead.Kind)
		}
	}
	if err := s.SetEdge(reportsEdge("other", "demo merger"), "owner"); err == nil {
		t.Fatal("an Agent may not report to a merger")
	}
}

// A restart that replaces a session moves its places in the tree: the Agent's
// lead session, the caller of its workers and its merger place.
func TestReplaceSessionMovesTheTreePlaces(t *testing.T) {
	s, merger := mergerRig(t)
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, merger).Scan(&node); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE agents SET lead_session=? WHERE id='demo merger'`, merger); err != nil {
		t.Fatal(err)
	}
	worker, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "w", Worker: &protocol.WorkerInfo{ID: "w_1", Owner: merger}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionOffline(node, merger); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "merge-2", Repo: protocol.RepoInfo{Root: "/w/demo", Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if moved, err := s.ReplaceSession(node, merger, fresh); err != nil || !moved {
		t.Fatalf("replace: %v %v", moved, err)
	}
	var lead, owner, place string
	if err := s.db.QueryRow(`SELECT (SELECT lead_session FROM agents WHERE id='demo merger'),
		(SELECT json_extract(worker,'$.owner') FROM sessions WHERE id=?),
		(SELECT session_id FROM project_mergers WHERE project='demo')`, worker).Scan(&lead, &owner, &place); err != nil {
		t.Fatal(err)
	}
	if lead != fresh || owner != fresh || place != fresh {
		t.Fatalf("lead_session=%q worker owner=%q merger=%q, want all %q", lead, owner, place, fresh)
	}
}

func TestSessionsRecordWhoStartedThem(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "owner", NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddProjects(node, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "repo", Path: "/code/repo"}}}); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveSessionAgent(node, protocol.SessionAgentReservation{Owner: owner, AgentID: "repo", Harness: "pi", NativeID: "chosen"}); err != nil {
		t.Fatal(err)
	}
	repo := protocol.RepoInfo{Root: "/code/repo", Project: "repo"}
	found, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "found", Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	chosen, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "chosen", Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "w", Worker: &protocol.WorkerInfo{ID: "w_1", Owner: found}})
	if err != nil {
		t.Fatal(err)
	}
	cto, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{found: protocol.StartedByOwner, chosen: protocol.StartedByAgentNet,
		worker: protocol.StartedByAgentNet, cto: protocol.StartedByAgentNet} {
		var got string
		if err := s.db.QueryRow(`SELECT started_by FROM sessions WHERE id=?`, id).Scan(&got); err != nil || got != want {
			t.Fatalf("session %s started_by=%q, want %q (%v)", id, got, want, err)
		}
	}
}

// orgFixture is a store shaped like the live gateway before the org tree:
// four Teams (one without a lead), repo_leads rows with and without a lead
// session, an Agent working in a Repo it does not own, a merger that runs as
// the Repo's Agent, the CTO and the chief of staff.
func orgFixture(t *testing.T) (*Store, map[string]string) {
	t.Helper()
	s := teamStore(t)
	ids, node := graphSessions(t, s, "agent-net", "tools", "homelab-agent", "infra", "finget", "tenebrae", "chief-of-staff", "solo", "misc")
	for _, native := range []string{"h2", "h3"} {
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: native, Repo: protocol.RepoInfo{Root: "/w/homelab-agent", Project: "homelab-agent"}})
		if err != nil {
			t.Fatal(err)
		}
		ids[native] = id
	}
	for native, project := range map[string]string{"merge": "agent-net", "visit": "solo"} {
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: native, Repo: protocol.RepoInfo{Root: "/w/" + project, Project: project}})
		if err != nil {
			t.Fatal(err)
		}
		ids[native] = id
	}
	worker, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "w", Worker: &protocol.WorkerInfo{ID: "w_1", Owner: ids["agent-net"]}})
	if err != nil {
		t.Fatal(err)
	}
	ids["worker"] = worker
	if ids["cto"], err = s.RegisterCTOSession(node); err != nil {
		t.Fatal(err)
	}
	if err := s.AddArchitect("helper", "owner"); err != nil {
		t.Fatal(err)
	}
	agent := func(id string) *protocol.GraphRef { return &protocol.GraphRef{Kind: "agent", ID: id} }
	repo := func(id string) *protocol.GraphRef { return &protocol.GraphRef{Kind: "repo", ID: id} }
	for _, w := range []protocol.TeamWrite{
		{Action: "create", ID: "core", Name: "Core", Owner: agent("agent-net")},
		{Action: "membership", ID: "core", Member: repo("tools")},
		{Action: "membership", ID: "core", Member: agent("helper")},
		{Action: "create", ID: "ops", Name: "Ops", Owner: agent("homelab-agent")},
		{Action: "membership", ID: "ops", Member: repo("infra")},
		{Action: "create", ID: "trade", Name: "Trade", Owner: agent("finget")},
		{Action: "membership", ID: "trade", Member: repo("tenebrae")},
		{Action: "create", ID: "loose", Name: "Loose"},
		{Action: "membership", ID: "loose", Member: repo("misc")},
	} {
		teamWrite(t, s, w)
	}
	if err := s.AddArchitect("visitor", "owner"); err != nil {
		t.Fatal(err)
	}
	// Today's rows, written as the old code wrote them.
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE sessions SET agent_id='visitor' WHERE id=?`, []any{ids["visit"]}},
		{`INSERT INTO repo_leads(repo_id,agent_id,lead_session) VALUES('homelab-agent','homelab-agent',?),('agent-net','agent-net',NULL)`, []any{ids["h2"]}},
		{`UPDATE teams SET lead_session=? WHERE id='ops'`, []any{ids["h2"]}},
		{`INSERT INTO project_mergers(project,node_id,session_id) VALUES('agent-net',?,?)`, []any{node, ids["merge"]}},
		{`INSERT INTO owner_settings(key,value,updated_at) VALUES('chief_session',?,0)`, []any{ids["chief-of-staff"]}},
	} {
		if _, err := s.db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	return s, ids
}

// rewindOrgTree puts the store back to the release before the org tree and
// runs the migrations again.
func rewindOrgTree(t *testing.T, s *Store) {
	t.Helper()
	version := migrationVersion(t, orgTreeMarker)
	for _, q := range []string{
		`DELETE FROM graph_edges WHERE kind='reports_to'`,
		`ALTER TABLE agents DROP COLUMN lead_session`,
		`ALTER TABLE sessions DROP COLUMN started_by`,
		`DELETE FROM owner_settings WHERE key='` + orgTreeBuiltKey + `'`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, version); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
}

func TestOrgTreeMigrationBuildsTheTreeFromTodaysData(t *testing.T) {
	s, ids := orgFixture(t)
	rewindOrgTree(t, s)
	for agent, parent := range map[string]string{
		"cto":              protocol.OrgRoot,
		"chief-of-staff":   protocol.OrgRoot,
		"helper":           "agent-net",
		"tools":            "agent-net",
		"infra":            "homelab-agent",
		"tenebrae":         "finget",
		"agent-net":        "cto",
		"homelab-agent":    "cto",
		"finget":           "cto",
		"visitor":          "solo",
		"misc":             "",
		"solo":             "",
		"agent-net merger": "agent-net",
	} {
		if got := reportsTo(t, s, agent); got != parent {
			t.Errorf("%s reports to %q, want %q", agent, got, parent)
		}
	}
	var lead, mergerAgent, role string
	if err := s.db.QueryRow(`SELECT (SELECT lead_session FROM agents WHERE id='homelab-agent'),
		(SELECT agent_id FROM sessions WHERE id=?),(SELECT role FROM agents WHERE id='agent-net merger')`, ids["merge"]).Scan(&lead, &mergerAgent, &role); err != nil {
		t.Fatal(err)
	}
	if lead != ids["h2"] || mergerAgent != "agent-net merger" || role != "merger" {
		t.Fatalf("lead_session=%q merger agent=%q role=%q", lead, mergerAgent, role)
	}
	for key, want := range map[string]string{"merge": protocol.StartedByAgentNet, "worker": protocol.StartedByAgentNet,
		"cto": protocol.StartedByAgentNet, "solo": protocol.StartedByOwner, "h3": protocol.StartedByOwner} {
		var got string
		if err := s.db.QueryRow(`SELECT started_by FROM sessions WHERE id=?`, ids[key]).Scan(&got); err != nil || got != want {
			t.Errorf("%s started_by=%q, want %q (%v)", key, got, want, err)
		}
	}
	a, ok, err := s.Agent(ids["merge"])
	if err != nil || !ok || a.RepoLead {
		t.Fatalf("the merger still counts as repo lead: %+v %v", a, err)
	}
}

func TestOrgTreeMigrationRunsOnce(t *testing.T) {
	s, _ := orgFixture(t)
	rewindOrgTree(t, s)
	if err := s.RemoveEdge(reportsEdge("helper", "agent-net"), "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, orgTreeMarker)); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	if got := reportsTo(t, s, "helper"); got != "" {
		t.Fatalf("a replay rebuilt a removed edge: helper reports to %q", got)
	}
}

// A merger runs as its own Agent now. A message sent to it from outside its
// Repo still reaches it, as when it ran as the Repo's Agent; it is not
// redirected to the Repo lead.
func TestLeadRouteKeepsAMessageToTheMerger(t *testing.T) {
	s, merger := mergerRig(t)
	var outsider string
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE native_id='n-other'`).Scan(&outsider); err != nil {
		t.Fatal(err)
	}
	m, _, route, err := s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: merger, Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != merger || route != "" {
		t.Fatalf("a message to the merger went to %s (%q)", m.To, route)
	}
}
