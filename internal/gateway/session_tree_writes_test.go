package gateway

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Decision 0193 part 2: the writes the CTO and the owner make keep the session
// tree right. Each scenario runs the real calls and draws the tree from the
// parent sessionParent gives every session, as the TUI does.

// treeRig is a gateway with one machine, its owner session and the CTO.
type treeRig struct {
	t           *testing.T
	s           *Store
	node, owner string
}

func newTreeRig(t *testing.T) *treeRig {
	t.Helper()
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterCTOSession(node); err != nil {
		t.Fatal(err)
	}
	// lead_set needs the typed Graph, as on a gateway whose machines upgraded.
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO gateway_state(key,value,updated_at) VALUES('graph_ref_gate','1',0)`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	return &treeRig{t: t, s: s, node: node, owner: owner}
}

// start registers a new session in project, as a harness the owner opened
// there does, and names it.
func (r *treeRig) start(name, project string) string {
	r.t.Helper()
	return r.register(name, protocol.SessionInfo{Harness: "claude", NativeID: name, Status: "idle",
		Repo: protocol.RepoInfo{Root: "/w/" + project, Project: project}})
}

// worker registers a worker of owner in project.
func (r *treeRig) worker(name, project, owner string) string {
	r.t.Helper()
	return r.register(name, protocol.SessionInfo{Harness: "claude", NativeID: name, Status: "busy",
		Repo:   protocol.RepoInfo{Root: "/w/" + project, Project: project},
		Worker: &protocol.WorkerInfo{ID: "w_" + name, Owner: owner}})
}

func (r *treeRig) register(name string, in protocol.SessionInfo) string {
	r.t.Helper()
	id, err := r.s.UpsertSession(r.node, in)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET name=?, name_fixed=1 WHERE id=?`, name, id); err != nil {
		r.t.Fatal(err)
	}
	return id
}

// agentOf is the Agent a session holds.
func (r *treeRig) agentOf(session string) string {
	r.t.Helper()
	var agent string
	if err := r.s.db.QueryRow(`SELECT COALESCE(agent_id,'') FROM sessions WHERE id=?`, session).Scan(&agent); err != nil {
		r.t.Fatal(err)
	}
	return agent
}

// tree draws the Sessions tree from the session list: each session under the
// parent the gateway computed, children by name, with "(no parent: <rule>)"
// where the rule could not place it. The owner session and replaced records,
// which are history, are left out.
func (r *treeRig) tree() string {
	r.t.Helper()
	rows, err := r.s.Agents(protocol.QueryAgents{})
	if err != nil {
		r.t.Fatal(err)
	}
	children := map[string][]protocol.AgentView{}
	for _, a := range rows {
		if protocol.IsOwner(a.Harness) || a.ReplacedBy != "" {
			continue
		}
		children[a.Parent] = append(children[a.Parent], a)
	}
	var b strings.Builder
	var draw func(parent, indent string)
	draw = func(parent, indent string) {
		kids := children[parent]
		slices.SortFunc(kids, func(x, y protocol.AgentView) int { return strings.Compare(x.Profile.Name, y.Profile.Name) })
		for _, a := range kids {
			b.WriteString(indent + a.Profile.Name)
			if a.NoParent != "" {
				b.WriteString(" (no parent: " + a.NoParent + ")")
			}
			b.WriteString("\n")
			draw(a.SessionID, indent+"  ")
		}
	}
	draw("", "")
	return b.String()
}

func (r *treeRig) wantTree(want string) {
	r.t.Helper()
	if got := r.tree(); got != want {
		r.t.Fatalf("session tree:\n%s\nwant:\n%s", got, want)
	}
}

// (a) The CTO sets a new lead for a project that has one. The new session
// takes the lead Agent and is the one lead session; the old lead and the
// members sit under it. The old lead's worker stays with the session that
// started it, since its daemon holds that caller for the hand-back.
func TestTreeSetANewLeadForAProject(t *testing.T) {
	r := newTreeRig(t)
	lead := r.start("shop-lead", "shop")
	member := r.start("shop-member", "shop")
	r.worker("shop-worker", "shop", lead)
	fresh := r.start("shop-new", "shop")
	if err := r.s.SetLead(protocol.LeadSetRequest{Ref: protocol.GraphRef{Kind: "repo", ID: "shop"},
		Lead: &protocol.GraphRef{Kind: "session", ID: fresh}}, "cto", false); err != nil {
		t.Fatal(err)
	}
	r.wantTree("CTO\n  shop-new\n    shop-lead\n      shop-worker\n    shop-member\n")
	if got := agentLeadSession(t, r.s, "shop"); got != fresh || r.agentOf(fresh) != "shop" {
		t.Fatalf("lead_session=%q, new lead holds %q; want %q on shop", got, r.agentOf(fresh), fresh)
	}
	if got := r.agentOf(member); got != "shop-agent" {
		t.Fatalf("the member holds %q, want shop-agent: only lead sessions hold the lead Agent", got)
	}
	var stood string
	if err := r.s.db.QueryRow(`SELECT stood_down_for FROM sessions WHERE id=?`, lead).Scan(&stood); err != nil || stood != fresh {
		t.Fatalf("old lead stood down for %q, want %q (%v)", stood, fresh, err)
	}
}

// (b) A new session in a project with a live lead takes the project's member
// Agent, created at once and reporting to the lead Agent, and sits under the
// lead: never under the CTO and never as a second lead.
func TestTreeNewSessionJoinsUnderTheLead(t *testing.T) {
	r := newTreeRig(t)
	lead := r.start("shop-lead", "shop")
	one := r.start("shop-one", "shop")
	two := r.start("shop-two", "shop")
	r.wantTree("CTO\n  shop-lead\n    shop-one\n    shop-two\n")
	for _, id := range []string{one, two} {
		if got := r.agentOf(id); got != "shop-agent" {
			t.Fatalf("new session holds %q, want the member Agent shop-agent", got)
		}
	}
	if got := reportsTo(t, r.s, "shop-agent"); got != "shop" {
		t.Fatalf("shop-agent reports to %q, want the lead Agent shop", got)
	}
	if got := agentLeadSession(t, r.s, "shop"); got != lead {
		t.Fatalf("lead_session=%q, want %q", got, lead)
	}
}

// (c) The owner replaces a busy lead. replaced_by is recorded and the lead's
// members and workers follow the new session. A second replace of the same
// session moves nothing and says why.
func TestTreeReplaceABusyLead(t *testing.T) {
	r := newTreeRig(t)
	lead := r.register("shop-lead", protocol.SessionInfo{Harness: "claude", NativeID: "shop-lead", Status: "busy",
		Repo: protocol.RepoInfo{Root: "/w/shop", Project: "shop"}})
	r.start("shop-member", "shop")
	r.worker("shop-worker", "shop", lead)
	fresh := r.start("shop-fresh", "shop")
	res, err := r.s.ReplaceSessionForOwner(lead, fresh, "cto")
	if err != nil || !res.Moved {
		t.Fatalf("replace: %+v %v", res, err)
	}
	var replacedBy string
	if err := r.s.db.QueryRow(`SELECT replaced_by FROM sessions WHERE id=?`, lead).Scan(&replacedBy); err != nil || replacedBy != fresh {
		t.Fatalf("replaced_by=%q, want %q (%v)", replacedBy, fresh, err)
	}
	// The new session carries the conversation's name.
	r.wantTree("CTO\n  shop-lead\n    shop-member\n    shop-worker\n")
	if got := agentLeadSession(t, r.s, "shop"); got != fresh {
		t.Fatalf("lead_session=%q, want the new session %q", got, fresh)
	}
	again, err := r.s.ReplaceSessionForOwner(lead, r.start("shop-third", "shop"), "cto")
	if err != nil || again.Moved || !strings.Contains(again.Reason, "already replaced by "+fresh) {
		t.Fatalf("second replace: %+v %v", again, err)
	}
}

// (d) The lead session stops, or is archived: the same call hands the lead to
// the next live session of the lead Agent by the I1 order (the one the owner
// used last before the oldest), and the members follow it.
func TestTreeLeadStopsOrIsArchived(t *testing.T) {
	for _, how := range []string{"stop", "archive"} {
		t.Run(how, func(t *testing.T) {
			r := newTreeRig(t)
			lead := r.start("shop-lead", "shop")
			r.start("shop-member", "shop")
			older := r.start("shop-older", "shop")
			used := r.start("shop-used", "shop")
			// The owner starts both for the lead Agent itself.
			for _, id := range []string{older, used} {
				if _, err := r.s.setSessionAgent(r.node, protocol.SessionAgentSetRequest{Owner: r.owner, SessionID: id, Agent: "shop"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.s.RecordOwnerUse(used); err != nil {
				t.Fatal(err)
			}
			if how == "stop" {
				if err := r.s.MarkSessionStopped(r.node, lead); err != nil {
					t.Fatal(err)
				}
				r.wantTree("CTO\n  shop-used\n    shop-lead\n    shop-member\n    shop-older\n")
			} else {
				if err := r.s.SetSessionOffline(r.node, lead); err != nil {
					t.Fatal(err)
				}
				if _, err := r.s.ArchiveSessions(protocol.SessionsArchiveRequest{IDs: []string{lead}}); err != nil {
					t.Fatal(err)
				}
				r.wantTree("CTO\n  shop-used\n    shop-member\n    shop-older\n")
			}
			if got := agentLeadSession(t, r.s, "shop"); got != used {
				t.Fatalf("lead_session=%q, want %q", got, used)
			}
		})
	}
}

// (e) The first session of a new project leads it and sits under the CTO;
// the project's later sessions sit under it.
func TestTreeNewLeadForANewProject(t *testing.T) {
	r := newTreeRig(t)
	lead := r.start("garden-lead", "garden")
	r.wantTree("CTO\n  garden-lead\n")
	later := r.start("garden-later", "garden")
	r.wantTree("CTO\n  garden-lead\n    garden-later\n")
	if r.agentOf(lead) != "garden" || r.agentOf(later) != "garden-agent" {
		t.Fatalf("agents: lead %q, later %q", r.agentOf(lead), r.agentOf(later))
	}
}

// I5: a start that names the CTO Agent would break I1 and is refused with one
// line that names the rule. Any other named Agent is accepted.
func TestTreeStartRefusedOnlyWhenItBreaksTheTree(t *testing.T) {
	r := newTreeRig(t)
	err := r.s.ReserveSessionAgent(r.node, protocol.SessionAgentReservation{Owner: r.owner, AgentID: "cto", Harness: "claude", NativeID: "n1"})
	if err == nil || !strings.Contains(err.Error(), "rule I1") || strings.Contains(err.Error(), "\n") {
		t.Fatalf("start for the CTO Agent: %v", err)
	}
	r.start("shop-lead", "shop")
	if err := r.s.ReserveSessionAgent(r.node, protocol.SessionAgentReservation{Owner: r.owner, AgentID: "shop", Harness: "claude", NativeID: "n2"}); err != nil {
		t.Fatalf("start for the lead Agent: %v", err)
	}
}

// The one migration of part 2 on the part 1 fixture (the scrubbed live
// gateway of 2026-11-26): the Agent with a grant but no lead session gets
// one, so its marker goes, and the old offline copy of a lead is archived.
// The report lists each change.
func TestTreeInvariantsMigration(t *testing.T) {
	s, _ := newTestStore(t)
	fixture, err := os.ReadFile(filepath.Join("..", "tui", "testdata", "session_tree_0193.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(string(fixture)); err != nil {
		t.Fatal(err)
	}
	// A lead Agent whose only session is offline has no lead session; the
	// migration leaves that session alone, and the tree draws it as the
	// Agent's lead without a marker (rule 5).
	if _, err := s.db.Exec(`INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone,lead_level) VALUES('idle-lead','idle-lead','',0,0,0,1);
		INSERT INTO sessions(id,node_id,harness,native_id,name,status,last_seen,online,agent_id) VALUES('s_i01','n_01','claude','native-s_i01','idle','idle',0,0,'idle-lead')`); err != nil {
		t.Fatal(err)
	}
	markers := func() map[string]string {
		rows, err := s.Agents(protocol.QueryAgents{})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, a := range rows {
			if a.NoParent != "" {
				out[a.SessionID] = a.NoParent
			}
		}
		return out
	}
	if got := markers(); got["s_d03"] != "no lead" {
		t.Fatalf("before the migration the fixture shows markers %v, want s_d03 with no lead", got)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.applyTreeInvariantsTx(tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Agent proj-d: lead session s_d03",
		"Session s_h02: archived, an offline copy of lead Agent proj-a-lead",
	}
	if !slices.Equal(report, want) {
		t.Fatalf("report:\n%s\nwant:\n%s", strings.Join(report, "\n"), strings.Join(want, "\n"))
	}
	if got := markers(); len(got) != 0 {
		t.Fatalf("markers left after the migration: %v, want none", got)
	}
	var archived, idle int64
	if err := s.db.QueryRow(`SELECT (SELECT archived_at FROM sessions WHERE id='s_h02'),(SELECT archived_at FROM sessions WHERE id='s_i01')`).Scan(&archived, &idle); err != nil || archived == 0 || idle != 0 {
		t.Fatalf("archived_at s_h02=%d s_i01=%d (%v), want only the copy s_h02 archived", archived, idle, err)
	}
	// A second run finds nothing left to change.
	tx, err = s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if again, err := s.applyTreeInvariantsTx(tx); err != nil || len(again) != 0 {
		t.Fatalf("second run: %v %v", again, err)
	}
}
