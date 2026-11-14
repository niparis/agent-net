package gateway

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// orgReadRig is a small org tree: lead reports to the owner, member to lead,
// chief to the owner. lead and member have two sessions each; the first is
// the Agent's lead session, and lead's is offline. chief holds asks over all
// and lead holds merge over member; the store seeds the CTO's admin and
// permissions over all.
func orgReadRig(t *testing.T) (*Store, map[string]string) {
	t.Helper()
	s := teamStore(t)
	ids, node := graphSessions(t, s, "lead", "member", "chief")
	for native, project := range map[string]string{"lead-2": "lead", "member-2": "member"} {
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: native, Repo: protocol.RepoInfo{Root: "/w/" + project, Project: project}})
		if err != nil {
			t.Fatal(err)
		}
		ids[native] = id
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE agents SET lead_session=? WHERE id='lead'`, []any{ids["lead"]}},
		{`UPDATE agents SET lead_session=? WHERE id='member'`, []any{ids["member"]}},
		{`UPDATE agents SET lead_session=NULL WHERE id='chief'`, nil},
		{`UPDATE sessions SET online=0 WHERE id=?`, []any{ids["lead"]}},
		{`UPDATE sessions SET started_by='agent-net' WHERE id=?`, []any{ids["member-2"]}},
		{`INSERT INTO profiles(session_id,name,updated_at) VALUES(?,'lead-main',0),(?,'member-main',0)`, []any{ids["lead"], ids["member"]}},
	} {
		if _, err := s.db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	for _, e := range []protocol.Edge{reportsEdge("lead", protocol.OrgRoot), reportsEdge("member", "lead"), reportsEdge("chief", protocol.OrgRoot)} {
		if err := s.SetEdge(e, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []protocol.ScopeSetRequest{
		scopeSet("chief", protocol.ScopeAsks, protocol.ScopeOverAll, "sort asks"),
		scopeSet("lead", protocol.ScopeMerge, "agent:member", ""),
	} {
		if _, err := s.SetScope(q, "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	return s, ids
}

func agentNodes(g protocol.Graph) map[string]protocol.Node {
	out := map[string]protocol.Node{}
	for _, n := range g.Nodes {
		if n.Ref != nil && n.Ref.Kind == "agent" {
			out[n.Ref.ID] = n
		}
	}
	return out
}

// The typed graph read is the one read of the org tree: each Agent carries
// its parent, its lead session, its sessions with who started them, and the
// scopes it holds.
func TestGraphReadCarriesTheOrgTree(t *testing.T) {
	s, ids := orgReadRig(t)
	g, err := s.GraphForRef(nil)
	if err != nil {
		t.Fatal(err)
	}
	agents := agentNodes(g)
	lead, member, chief := agents["lead"], agents["member"], agents["chief"]
	if lead.ReportsTo != protocol.OrgRoot || member.ReportsTo != "lead" || chief.ReportsTo != protocol.OrgRoot || agents["demo"].ReportsTo != "" {
		t.Fatalf("reports_to: lead=%q member=%q chief=%q demo=%q", lead.ReportsTo, member.ReportsTo, chief.ReportsTo, agents["demo"].ReportsTo)
	}
	if want := (&protocol.GraphSession{ID: ids["lead"], Name: "lead-main", Online: false, StartedBy: protocol.StartedByOwner}); !reflect.DeepEqual(lead.LeadSession, want) {
		t.Fatalf("lead's lead session = %+v, want %+v", lead.LeadSession, want)
	}
	if member.LeadSession == nil || member.LeadSession.ID != ids["member"] || member.LeadSession.Name != "member-main" || !member.LeadSession.Online {
		t.Fatalf("member's lead session = %+v", member.LeadSession)
	}
	if chief.LeadSession != nil {
		t.Fatalf("chief has no lead session, got %+v", chief.LeadSession)
	}
	started := map[string]string{}
	for _, session := range member.Sessions {
		if !session.Online {
			t.Fatalf("listed session %+v is not online", session)
		}
		started[session.ID] = session.StartedBy
	}
	if want := map[string]string{ids["member"]: protocol.StartedByOwner, ids["member-2"]: protocol.StartedByAgentNet}; !reflect.DeepEqual(started, want) {
		t.Fatalf("member sessions started_by = %v, want %v", started, want)
	}
	scopes := func(n protocol.Node) []string {
		var out []string
		for _, sc := range n.Scopes {
			out = append(out, sc.Kind+" "+sc.Over+" "+sc.Note)
		}
		sort.Strings(out)
		return out
	}
	if got := scopes(chief); !reflect.DeepEqual(got, []string{"asks all sort asks"}) {
		t.Fatalf("chief scopes = %v", got)
	}
	if got := scopes(lead); !reflect.DeepEqual(got, []string{"merge agent:member "}) {
		t.Fatalf("lead scopes = %v", got)
	}
	if len(member.Scopes) != 0 {
		t.Fatalf("member scopes = %+v", member.Scopes)
	}
}

// Every session the TUI lists carries the tree parent of its Agent, so the
// TUI reads the same hierarchy as the graph and routing.
func TestAgentListCarriesTheTreeParent(t *testing.T) {
	s, ids := orgReadRig(t)
	agents, err := s.Agents(protocol.QueryAgents{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, a := range agents {
		got[a.SessionID] = a.ReportsTo
	}
	for session, want := range map[string]string{
		ids["lead"]:     protocol.OrgRoot,
		ids["member"]:   "lead",
		ids["member-2"]: "lead",
		ids["chief"]:    protocol.OrgRoot,
	} {
		if got[session] != want {
			t.Fatalf("session %s reports to %q, want %q (all: %v)", session, got[session], want, got)
		}
	}
}

// root limits graph.get and graph.read to one subtree of the org tree.
func TestGraphReadRootIsOneSubtree(t *testing.T) {
	s, _ := orgReadRig(t)
	g := &Gateway{store: s}
	for _, frame := range []string{"graph.get", "graph.read"} {
		raw, _ := json.Marshal(map[string]any{"root": "lead"})
		out, err := g.handle(context.Background(), typedNode("n"), frame, raw)
		if err != nil {
			t.Fatalf("%s: %v", frame, err)
		}
		b, _ := json.Marshal(out)
		var graph protocol.Graph
		if frame == "graph.read" {
			var got protocol.GraphGot
			err = json.Unmarshal(b, &got)
			graph = got.Graph
		} else {
			err = json.Unmarshal(b, &graph)
		}
		if err != nil {
			t.Fatalf("%s: %s: %v", frame, b, err)
		}
		var names []string
		for _, n := range graph.Nodes {
			id := n.Name
			if n.Ref != nil {
				id = n.Ref.ID
			}
			names = append(names, n.Kind+":"+id)
		}
		sort.Strings(names)
		if want := []string{"agent:lead", "agent:member"}; !reflect.DeepEqual(names, want) {
			t.Fatalf("%s root lead = %v, want %v", frame, names, want)
		}
	}
}

// agent_session_get says whom a session reports to and who else it answers
// to, both read from the stored tree and scopes. A session that is not its
// Agent's lead session reports to that lead session first.
func TestSessionGetShowsWhoTheSessionReportsTo(t *testing.T) {
	s, ids := orgReadRig(t)
	for _, c := range []struct{ session, reportsTo, also string }{
		{ids["member-2"], "member", "chief (asks), cto (admin, permissions), lead (merge)"},
		{ids["member"], "lead", "chief (asks), cto (admin, permissions), lead (merge)"},
		{ids["lead"], "owner", "chief (asks), cto (admin, permissions)"},
		{ids["chief"], "owner", "cto (admin, permissions)"},
	} {
		got, err := s.GetSession(protocol.SessionGetRequest{Session: c.session})
		if err != nil {
			t.Fatal(err)
		}
		if got.Session.ReportsTo != c.reportsTo || got.Session.AlsoAnswersTo != c.also {
			t.Fatalf("session %s: reports_to=%q also_answers_to=%q, want %q and %q", c.session, got.Session.ReportsTo, got.Session.AlsoAnswersTo, c.reportsTo, c.also)
		}
	}
}
