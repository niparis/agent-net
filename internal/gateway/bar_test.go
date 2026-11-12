package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// barFixture is a store with the repo "demo", its lead session, a peer session
// of another agent in the same repo, a worker, the owner and the CTO.
type barFixture struct {
	s                              *Store
	lead, peer, worker, owner, cto string
}

func newBarFixture(t *testing.T) barFixture {
	t.Helper()
	s := teamStore(t)
	f := barFixture{s: s, lead: draftSession(t, s, "demo")}
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, f.lead).Scan(&node); err != nil {
		t.Fatal(err)
	}
	add := func(info protocol.SessionInfo) string {
		id, _, err := s.upsertSession(s.db, node, info, true)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	repo := protocol.RepoInfo{Root: "/w/demo", Project: "demo"}
	f.peer = add(protocol.SessionInfo{Harness: "pi", NativeID: "peer", Repo: repo})
	// The peer works for another agent, so it is not the lead of demo.
	f.worker = add(protocol.SessionInfo{Harness: "claude", NativeID: "worker", Repo: repo})
	f.owner = add(protocol.SessionInfo{Harness: "owner", NativeID: "owner"})
	f.cto = add(protocol.SessionInfo{Harness: "cto", NativeID: "cto", Name: "CTO"})
	for _, q := range []struct{ query, id string }{
		{`UPDATE sessions SET agent_id='other' WHERE id=?`, f.peer},
		{`UPDATE sessions SET worker='{"owner":"` + f.lead + `"}' WHERE id=?`, f.worker},
	} {
		if _, err := s.db.Exec(q.query, q.id); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f barFixture) edit(t *testing.T, session string, q protocol.BarEditRequest) protocol.BarEditResult {
	t.Helper()
	res, err := f.s.EditBar(session, q)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	return res
}

func barIDs(rules []protocol.BarRule) string {
	var ids []string
	for _, r := range rules {
		ids = append(ids, r.ID)
	}
	return strings.Join(ids, ",")
}

func TestBarLeadSeedsEditsAndRemovesRules(t *testing.T) {
	f := newBarFixture(t)
	res := f.edit(t, f.lead, protocol.BarEditRequest{Add: []string{"Performance first.", "  Migrations   are append-only. ", "No scratch files."}})
	if res.Project != "demo" || barIDs(res.Rules) != "B1,B2,B3" || res.Rules[1].Text != "Migrations are append-only." {
		t.Fatalf("seed: %+v", res)
	}
	for _, r := range res.Rules {
		if r.Source != protocol.BarSourceSeeded || r.CreatedAt == 0 {
			t.Fatalf("rule %+v: want source seeded and a creation time", r)
		}
	}
	// One call may mix all three; IDs of removed rules are not given out again.
	res = f.edit(t, f.lead, protocol.BarEditRequest{Add: []string{"Decision numbers are unique."}, Edit: []protocol.BarEdit{{ID: "b1", Text: "Performance first, always."}}, Remove: []string{"B2"}})
	if barIDs(res.Rules) != "B1,B3,B4" || res.Rules[0].Text != "Performance first, always." || res.Rules[2].Source != protocol.BarSourceLead {
		t.Fatalf("mixed edit: %+v", res)
	}
	res = f.edit(t, f.lead, protocol.BarEditRequest{Remove: []string{"B4"}})
	res = f.edit(t, f.lead, protocol.BarEditRequest{Add: []string{"Next."}})
	if barIDs(res.Rules) != "B1,B3,B5" {
		t.Fatalf("a removed last ID was reused: %+v", res)
	}
}

func TestBarEditIsAtomic(t *testing.T) {
	f := newBarFixture(t)
	f.edit(t, f.lead, protocol.BarEditRequest{Add: []string{"One."}})
	_, err := f.s.EditBar(f.lead, protocol.BarEditRequest{Add: []string{"Two."}, Edit: []protocol.BarEdit{{ID: "B9", Text: "Nope."}}})
	if err == nil {
		t.Fatal("edit of a missing rule accepted")
	}
	if rules, _ := f.s.BarRules("demo"); barIDs(rules) != "B1" {
		t.Fatalf("a refused call changed the bar: %+v", rules)
	}
	for name, q := range map[string]protocol.BarEditRequest{
		"empty call": {},
		"long rule":  {Add: []string{strings.Repeat("x", protocol.MaxBarRuleLen+1)}},
		"blank rule": {Add: []string{"  "}},
		"bad ID":     {Remove: []string{"3"}},
	} {
		if _, err := f.s.EditBar(f.lead, q); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestBarPermissions(t *testing.T) {
	f := newBarFixture(t)
	f.edit(t, f.lead, protocol.BarEditRequest{Add: []string{"One."}})
	for name, session := range map[string]string{"a peer that is not the lead": f.peer, "a worker of the lead": f.worker} {
		if _, err := f.s.EditBar(session, protocol.BarEditRequest{Add: []string{"Sneaky."}}); err == nil {
			t.Errorf("%s changed the bar", name)
		}
		if _, err := f.s.EditBar(session, protocol.BarEditRequest{Remove: []string{"B1"}}); err == nil {
			t.Errorf("%s removed a rule", name)
		}
	}
	// A lead edits its own project only.
	if _, err := f.s.EditBar(f.lead, protocol.BarEditRequest{Project: "other", Add: []string{"Elsewhere."}}); err == nil {
		t.Error("a lead changed another project's bar")
	}
	// The owner and the CTO name the project, and write as the owner.
	for _, session := range []string{f.owner, f.cto} {
		if _, err := f.s.EditBar(session, protocol.BarEditRequest{Add: []string{"No project."}}); err == nil {
			t.Error("owner or CTO edit without a project accepted")
		}
		if _, err := f.s.EditBar(session, protocol.BarEditRequest{Project: "nowhere", Add: []string{"Lost."}}); err == nil {
			t.Error("edit of an unknown project accepted")
		}
	}
	res := f.edit(t, f.owner, protocol.BarEditRequest{Project: "demo", Add: []string{"Owner rule."}})
	res = f.edit(t, f.cto, protocol.BarEditRequest{Project: "demo", Remove: []string{"B1"}})
	if barIDs(res.Rules) != "B2" || res.Rules[0].Source != protocol.BarSourceOwner {
		t.Fatalf("owner and CTO edits: %+v", res)
	}
	// The rules changed by nobody else.
	if rules, _ := f.s.BarRules("demo"); barIDs(rules) != "B2" {
		t.Fatalf("bar: %+v", rules)
	}
}

func TestBarShowsInTheGraph(t *testing.T) {
	f := newBarFixture(t)
	f.edit(t, f.lead, protocol.BarEditRequest{Add: []string{"Performance first."}})
	g, err := f.s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		switch n.Name {
		case "demo":
			if barIDs(n.Bar) != "B1" || n.Bar[0].Text != "Performance first." {
				t.Fatalf("demo bar: %+v", n.Bar)
			}
		case "other":
			if len(n.Bar) != 0 {
				t.Fatalf("a project without a bar has one: %+v", n.Bar)
			}
		}
	}
	// An edit shows at once: the graph read is kept only until a table changes.
	f.edit(t, f.lead, protocol.BarEditRequest{Add: []string{"Second."}})
	g, _ = f.s.Graph()
	for _, n := range g.Nodes {
		if n.Name == "demo" && barIDs(n.Bar) != "B1,B2" {
			t.Fatalf("stale graph: %+v", n.Bar)
		}
	}
}

func TestBarIsInTheAcceptAndReviewTasks(t *testing.T) {
	f := newBarFixture(t)
	for _, step := range []string{"accept", "review"} {
		if strings.Contains(f.s.stepContract("demo", step), "Project rules") {
			t.Fatalf("%s task lists rules for a project without a bar", step)
		}
	}
	f.edit(t, f.lead, protocol.BarEditRequest{Add: []string{"Performance first.", "No scratch files."}})
	for _, step := range []string{"accept", "review"} {
		got := f.s.stepContract("demo", step)
		for _, want := range []string{"Project rules (check the diff against each):", "  B1: Performance first.", "  B2: No scratch files."} {
			if !strings.Contains(got, want) {
				t.Errorf("%s task lacks %q:\n%s", step, want, got)
			}
		}
	}
	if got := f.s.stepContract("demo", "test"); strings.Contains(got, "Project rules") {
		t.Errorf("test step lists the bar:\n%s", got)
	}
	if got := f.s.stepContract("other", "accept"); strings.Contains(got, "Project rules") {
		t.Errorf("another project's accept task lists the bar:\n%s", got)
	}
}

func TestBarRequestToTheLeadOncePerAgentsMD(t *testing.T) {
	f := newBarFixture(t)
	text := mustAsk(t, f.s, f.lead, "h1")
	for _, want := range []string{"Project rules request", "agent_bar_edit", "5 to 15", "AGENTS.md"} {
		if !strings.Contains(text, want) {
			t.Fatalf("request lacks %q:\n%s", want, text)
		}
	}
	if again := mustAsk(t, f.s, f.lead, "h1"); strings.Contains(again, "Project rules request") {
		t.Fatalf("asked twice for the same AGENTS.md:\n%s", again)
	}
	if next := mustAsk(t, f.s, f.lead, "h2"); !strings.Contains(next, "Project rules request") {
		t.Fatalf("not asked after AGENTS.md changed:\n%s", next)
	}
	// Only the lead is asked, and nobody once the project has a bar.
	if got := mustAsk(t, f.s, f.peer, "h9"); strings.Contains(got, "Project rules request") {
		t.Fatalf("a session that is not the lead was asked:\n%s", got)
	}
	if got := mustAsk(t, f.s, f.worker, "h9"); strings.Contains(got, "Project rules request") {
		t.Fatalf("a worker was asked:\n%s", got)
	}
	f.edit(t, f.lead, protocol.BarEditRequest{Add: []string{"One."}})
	if got := mustAsk(t, f.s, f.lead, "h3"); strings.Contains(got, "Project rules request") {
		t.Fatalf("asked for a project that has a bar:\n%s", got)
	}
	// No AGENTS.md, no request.
	if got := mustAsk(t, f.s, draftSession(t, f.s, "other"), ""); got != "" {
		t.Fatalf("asked without AGENTS.md:\n%s", got)
	}
}
