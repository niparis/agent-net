package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// botAgentFixture opens the typed Graph, makes the repo "ingest-forge" with a
// shared MCP server of the same name on host "research", and a bot app
// "cursor".
func botAgentFixture(t *testing.T) (*Store, App) {
	t.Helper()
	s, _ := newTestStore(t)
	if _, err := s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	if err := s.GraphLifecycle("repo.create", protocol.GraphLifecycleRequest{Name: "ingest-forge"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	node, _, err := s.CreateNode("research", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AddMCPServer(mcpSpec("ingest-forge", node)); err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp("cursor", "bot", "c1", "Cursor", nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, app
}

func botNode(t *testing.T, s *Store, id string) (protocol.Node, bool) {
	t.Helper()
	g, err := s.GraphForRef(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		if n.Ref != nil && n.Ref.Kind == "agent" && n.Ref.ID == id {
			return n, true
		}
	}
	return protocol.Node{}, false
}

func TestBotAppGetsAnAgentNodeWhenApproved(t *testing.T) {
	s, app := botAgentFixture(t)
	n, ok := botNode(t, s, app.ID)
	if !ok || n.Name != "cursor" || n.Role != "bot" {
		t.Fatalf("bot node = %+v, %v", n, ok)
	}
	if n.Bot == nil || n.Bot.Client != "Cursor" || n.Bot.ConnectedAt == 0 {
		t.Fatalf("bot details = %+v", n.Bot)
	}
	owner, err := s.CreateApp("phone", "owner_app", "c2", "Phone", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := botNode(t, s, owner.ID); ok {
		t.Fatal("an owner app got a node")
	}
}

func TestBotNodeFollowsRenameAndIsArchivedOnRevoke(t *testing.T) {
	s, app := botAgentFixture(t)
	forge := protocol.GraphRef{Kind: "agent", ID: app.ID}
	if err := s.WriteTeam(protocol.TeamWrite{Action: "create", ID: "forge", Name: "forge"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTeam(protocol.TeamWrite{Action: "membership", ID: "forge", Member: &forge}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameApp(app.ID, "Trade"); err != nil {
		t.Fatal(err)
	}
	if n, ok := botNode(t, s, app.ID); !ok || n.Name != "Trade" {
		t.Fatalf("after rename: %+v, %v", n, ok)
	}
	if err := s.RevokeApp(app.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := botNode(t, s, app.ID); ok {
		t.Fatal("revoked bot is still in the Graph")
	}
	var tomb int
	if err := s.db.QueryRow(`SELECT tombstone FROM agents WHERE id=?`, app.ID).Scan(&tomb); err != nil || tomb != 1 {
		t.Fatalf("agent row is not archived: %d, %v", tomb, err)
	}
	if team, err := s.MemberTeam(forge); err != nil || team != "" {
		t.Fatalf("revoked bot is still in team %q: %v", team, err)
	}
	g, err := s.GraphForRef(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range g.Edges {
		if e.Kind == "member_of" && e.From == app.ID {
			t.Fatalf("dangling member edge %+v", e)
		}
	}
	if len(g.Warnings) != 0 {
		t.Fatalf("warnings %v", g.Warnings)
	}
	// A new app that takes the old name makes a new node, with no warning.
	next, err := s.CreateApp("Trade", "bot", "c3", "Cursor", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := botNode(t, s, next.ID); !ok || n.Name != "Trade" {
		t.Fatalf("new app node: %+v, %v", n, ok)
	}
	if g, _ = s.GraphForRef(nil); len(g.Warnings) != 0 {
		t.Fatalf("warnings %v", g.Warnings)
	}
}

func TestBotNodeIsBackfilledForExistingApps(t *testing.T) {
	s, _ := botAgentFixture(t)
	// An app approved before this release has no node and an unlinked session.
	for _, q := range []string{
		`DROP TRIGGER bot_agent_insert`,
		`INSERT INTO apps (id,name,kind,client_id,client_name,redirect_uris,created_at,grants) VALUES ('app_old','Old','bot','c9','Old client','[]',5,'[]')`,
		`INSERT INTO apps (id,name,kind,client_id,client_name,redirect_uris,created_at,revoked_at,grants) VALUES ('app_gone','Gone','bot','c8','Gone client','[]',5,6,'[]')`,
		`INSERT INTO apps (id,name,kind,client_id,client_name,redirect_uris,created_at,grants) VALUES ('app_phone','Phone','owner_app','c7','Phone client','[]',5,'[]')`,
		`INSERT INTO sessions (id,node_id,harness,native_id,name,last_seen,online) VALUES ('s_old','app_old','bot','app_old','Old',5,1)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := s.db.Exec(botAgentBackfill); err != nil {
		t.Fatal(err)
	}
	if n, ok := botNode(t, s, "app_old"); !ok || n.Name != "Old" || n.Role != "bot" {
		t.Fatalf("backfilled node: %+v, %v", n, ok)
	}
	for _, id := range []string{"app_gone", "app_phone"} {
		if _, ok := botNode(t, s, id); ok {
			t.Fatalf("%s got a node", id)
		}
	}
	var agent string
	if err := s.db.QueryRow(`SELECT COALESCE(agent_id,'') FROM sessions WHERE id='s_old'`).Scan(&agent); err != nil || agent != "app_old" {
		t.Fatalf("session agent = %q, %v", agent, err)
	}
	// Running the backfill again changes nothing.
	if _, err := s.db.Exec(botAgentBackfill); err != nil {
		t.Fatal(err)
	}
}

func TestBotSessionLinksToItsNode(t *testing.T) {
	s, app := botAgentFixture(t)
	id, err := s.touchBot(app)
	if err != nil {
		t.Fatal(err)
	}
	var agent string
	if err = s.db.QueryRow(`SELECT COALESCE(agent_id,'') FROM sessions WHERE id=?`, id).Scan(&agent); err != nil || agent != app.ID {
		t.Fatalf("session agent = %q, %v", agent, err)
	}
	n, ok := botNode(t, s, app.ID)
	if !ok || n.SessionCount != 1 || len(n.Sessions) != 1 || n.Sessions[0].ID != id {
		t.Fatalf("node sessions: %+v", n)
	}
}

func TestBotNodeJoinsATeamButCannotLeadOne(t *testing.T) {
	s, app := botAgentFixture(t)
	bot := protocol.GraphRef{Kind: "agent", ID: app.ID}
	if err := s.WriteTeam(protocol.TeamWrite{Action: "create", ID: "forge", Name: "forge"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTeam(protocol.TeamWrite{Action: "membership", ID: "forge", Member: &bot}, "owner"); err != nil {
		t.Fatal(err)
	}
	if team, err := s.MemberTeam(bot); err != nil || team != "forge" {
		t.Fatalf("team = %q, %v", team, err)
	}
	g, err := s.GraphForRef(nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range g.Edges {
		if e.Kind == "member_of" && e.FromRef != nil && *e.FromRef == bot && e.To == "forge" {
			found = true
		}
	}
	if !found {
		t.Fatal("no member_of edge for the bot")
	}
	// Lead refusals: set lead, create with owner, lead by a bot session.
	err = s.SetLead(protocol.LeadSetRequest{Ref: protocol.GraphRef{Kind: "team", ID: "forge"}, Lead: &bot}, "owner", false)
	if err == nil || !strings.Contains(err.Error(), "bot can never") {
		t.Fatalf("set lead error = %v", err)
	}
	err = s.WriteTeam(protocol.TeamWrite{Action: "create", ID: "two", Name: "two", Owner: &bot}, "owner")
	if err == nil || !strings.Contains(err.Error(), "bot can never") {
		t.Fatalf("team owner error = %v", err)
	}
	session, err := s.touchBot(app)
	if err != nil {
		t.Fatal(err)
	}
	err = s.SetLead(protocol.LeadSetRequest{Ref: protocol.GraphRef{Kind: "team", ID: "forge"}, Lead: &protocol.GraphRef{Kind: "session", ID: session}}, "owner", false)
	if err == nil || !strings.Contains(err.Error(), "bot can never") {
		t.Fatalf("session lead error = %v", err)
	}
}

func TestBotCallsEdgeIsDerivedFromGrants(t *testing.T) {
	s, app := botAgentFixture(t)
	calls := func() []protocol.Edge {
		g, err := s.GraphForRef(nil)
		if err != nil {
			t.Fatal(err)
		}
		var out []protocol.Edge
		for _, e := range g.Edges {
			if e.Kind == "calls" {
				out = append(out, e)
			}
		}
		return out
	}
	if len(calls()) != 0 {
		t.Fatal("calls edge before any grant")
	}
	if err := s.SetAppGrant(app.ID, protocol.MCPServerGrant("ingest-forge"), true); err != nil {
		t.Fatal(err)
	}
	edges := calls()
	if len(edges) != 1 {
		t.Fatalf("calls edges = %+v", edges)
	}
	e := edges[0]
	if e.From != app.ID || e.To != "ingest-forge" || e.FromRef.Kind != "agent" || e.ToRef.Kind != "repo" || e.Rule != "" {
		t.Fatalf("calls edge = %+v", e)
	}
	n, _ := botNode(t, s, app.ID)
	if n.Bot == nil || len(n.Bot.CanCall) != 1 || n.Bot.CanCall[0] != "ingest-forge" {
		t.Fatalf("can call = %+v", n.Bot)
	}
	var stored int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM graph_edges WHERE kind='calls'`).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("stored calls edges = %d, %v", stored, err)
	}
	if err := s.SetAppGrant(app.ID, protocol.MCPServerGrant("ingest-forge"), false); err != nil {
		t.Fatal(err)
	}
	if len(calls()) != 0 {
		t.Fatal("calls edge after the grant was removed")
	}
}

func TestBotCallsEdgeFindsTheProjectOfTheServer(t *testing.T) {
	s, app := botAgentFixture(t)
	_, node := graphSessions(t, s, "library")
	if err := s.AddMCPServer(mcpSpec("tenebrae-library", node)); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMCPServer(mcpSpec("stray", node)); err != nil {
		t.Fatal(err)
	}
	// The assignment names one project for the first server; the second has
	// neither an assignment nor a repo of its name.
	id, _, err := s.MCPItemID("tenebrae-library")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AssignTool(id, protocol.ToolAssignment{Scope: "projects", Projects: []string{"library"}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tenebrae-library", "stray"} {
		if err := s.SetAppGrant(app.ID, protocol.MCPServerGrant(name), true); err != nil {
			t.Fatal(err)
		}
	}
	g, err := s.GraphForRef(nil)
	if err != nil {
		t.Fatal(err)
	}
	var targets []string
	for _, e := range g.Edges {
		if e.Kind == "calls" {
			targets = append(targets, e.To)
		}
	}
	if len(targets) != 1 || targets[0] != "library" {
		t.Fatalf("calls targets = %v", targets)
	}
	n, _ := botNode(t, s, app.ID)
	if n.Bot == nil || len(n.Bot.CanCall) != 2 {
		t.Fatalf("can call = %+v", n.Bot)
	}
}

func TestGraphEditsRefuseTheCallsEdge(t *testing.T) {
	s, app := botAgentFixture(t)
	e := protocol.Edge{From: app.ID, To: "ingest-forge", Kind: "calls", Rule: "normal",
		FromRef: &protocol.GraphRef{Kind: "agent", ID: app.ID}, ToRef: &protocol.GraphRef{Kind: "repo", ID: "ingest-forge"}}
	if err := s.SetEdge(e, "owner"); err == nil || !strings.Contains(err.Error(), "calls") {
		t.Fatalf("SetEdge error = %v", err)
	}
	if err := s.SetAppGrant(app.ID, protocol.MCPServerGrant("ingest-forge"), true); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveEdge(e, "owner"); err == nil || !strings.Contains(err.Error(), "calls") {
		t.Fatalf("RemoveEdge error = %v", err)
	}
}

func TestBotNodeCannotBeChosenAsASessionAgent(t *testing.T) {
	s, app := botAgentFixture(t)
	_, node := graphSessions(t, s, "demo")
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "owner", NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := s.sessionAgentOptions(node, owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range opts.Agents {
		if a.ID == app.ID {
			t.Fatal("a bot is offered as a session agent")
		}
	}
	session, _ := graphSessions(t, s, "other")
	err = s.setSessionAgent(node, protocol.SessionAgentSetRequest{Owner: owner, SessionID: session["other"], Agent: app.ID})
	if err == nil {
		t.Fatal("a session was linked to a bot node")
	}
}

// A bot session routes like an agent session: outside the crew its request
// goes to the Team lead, and once the bot node joins the Team it is in the crew.
func TestBotSessionRoutesLikeAnAgentInATeam(t *testing.T) {
	s := teamStore(t)
	var target, lead string
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='demo'`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='other'`).Scan(&lead); err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp("cursor", "bot", "c1", "Cursor", nil)
	if err != nil {
		t.Fatal(err)
	}
	botSession, err := s.touchBot(app)
	if err != nil {
		t.Fatal(err)
	}
	repo := protocol.GraphRef{Kind: "repo", ID: "demo"}
	owner := protocol.GraphRef{Kind: "agent", ID: "other"}
	bot := protocol.GraphRef{Kind: "agent", ID: app.ID}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Owner: &owner})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &repo})
	if err := s.SetLead(protocol.LeadSetRequest{Ref: repo}, "owner", false); err != nil {
		t.Fatal(err)
	}
	m, _, route, err := s.StoreLeadMessage(protocol.MsgSend{Sender: botSession, Recipient: target, Body: "hello"})
	if err != nil || m.To != lead || !strings.Contains(route, "Redirected") {
		t.Fatalf("outside the crew: %+v %q %v", m, route, err)
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &bot})
	m, _, route, err = s.StoreLeadMessage(protocol.MsgSend{Sender: botSession, Recipient: target, Body: "hello again"})
	if err != nil || m.To != target || strings.Contains(route, "Redirected") {
		t.Fatalf("in the crew: %+v %q %v", m, route, err)
	}
}
