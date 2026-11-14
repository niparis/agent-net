package gateway

import (
	"strings"
	"sync/atomic"
	"testing"

	delegated "github.com/niparis/agent-net/internal/authority"
	"github.com/niparis/agent-net/internal/protocol"
)

// A scope write changes what a session may do, so the daemons must drop the
// policy they cached, or a removed scope would keep allowing for seconds.
func TestScopeWriteTellsDaemonsToDropCaches(t *testing.T) {
	s := teamStore(t)
	var n atomic.Int32
	s.charterHook = func() { n.Add(1) }
	if _, err := s.SetScope(scopeSet("demo", protocol.ScopeMerge, "repo:demo", ""), "owner", true); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 0 {
		t.Fatalf("a validation run notified %d times", n.Load())
	}
	if _, err := s.SetScope(scopeSet("demo", protocol.ScopeMerge, "repo:demo", ""), "owner", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveScope(scopeSet("demo", protocol.ScopeMerge, "repo:demo", ""), "owner", false); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 {
		t.Fatalf("set and remove notified %d times, want 2", n.Load())
	}
	if _, err := s.RemoveScope(scopeSet("demo", protocol.ScopeMerge, "repo:demo", ""), "owner", false); err == nil {
		t.Fatal("removing a missing scope succeeded")
	}
	if n.Load() != 2 {
		t.Fatalf("a refused write notified: %d", n.Load())
	}
}

// policy.get carries the scopes of the session's role node and the delegated
// authority text built from them. A session with no scope gets neither.
func TestPolicyGetCarriesScopesAndDelegatedText(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := r.s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`INSERT INTO agent_charter VALUES('demo','purpose','Agent goal',1,'owner')`); err != nil {
		t.Fatal(err)
	}
	policy := func() protocol.PolicyGot {
		t.Helper()
		got, err := deliveryCall[protocol.PolicyGot](r, r.a, "policy.get", protocol.PolicyGet{SessionID: r.caller})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := policy()
	if len(got.Scopes) != 0 || strings.Contains(got.Charter, "Delegated authority") || !strings.Contains(got.Charter, "Agent goal") {
		t.Fatalf("a session with no scope: scopes %v, charter %q", got.Scopes, got.Charter)
	}
	for _, kind := range []string{protocol.ScopeMerge, protocol.ScopeRelease} {
		if _, err := r.s.SetScope(scopeSet("demo", kind, "repo:demo", ""), "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	got = policy()
	if len(got.Scopes) != 2 || got.Scopes[0].Kind != protocol.ScopeMerge || got.Scopes[0].Over != "repo:demo" {
		t.Fatalf("scopes = %v", got.Scopes)
	}
	if !strings.Contains(got.Charter, "Delegated authority: you may merge and release in demo.") || !strings.Contains(got.Charter, "Agent goal") {
		t.Fatalf("charter = %q", got.Charter)
	}
}

// A team scope reaches the repos of the team, and an agent scope the repos the
// agent owns, so the daemon can match a folder without a graph read.
func TestScopeReposResolveTeamAndAgentAreas(t *testing.T) {
	s := teamStore(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha"})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})
	for _, over := range []string{"team:alpha", "agent:other"} {
		if _, err := s.SetScope(scopeSet("demo", protocol.ScopeMerge, over, ""), "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	scopes, repos, err := s.ScopesWithRepos("demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 2 {
		t.Fatalf("scopes = %v", scopes)
	}
	if got := strings.Join(repos["team:alpha"], ","); got != "demo" {
		t.Fatalf("team repos = %q", got)
	}
	if got := strings.Join(repos["agent:other"], ","); got != "other" {
		t.Fatalf("agent repos = %q", got)
	}
}

// The gateway refuses the CTO's own lifecycle calls that the one owner-only
// list names, and no others.
func TestCTOLifecycleRefusalReadsTheNeverList(t *testing.T) {
	s := teamStore(t)
	refused := delegated.LifecycleActions()
	if len(refused) == 0 {
		t.Fatal("the list names no lifecycle action")
	}
	for _, action := range refused {
		err := s.GraphLifecycle(action, protocol.GraphLifecycleRequest{}, "cto", true)
		if err == nil || !strings.Contains(err.Error(), "owner approval required") {
			t.Errorf("%s from the CTO: %v", action, err)
		}
	}
	err := s.GraphLifecycle("repo.create", protocol.GraphLifecycleRequest{}, "cto", true)
	if err != nil && strings.Contains(err.Error(), "owner approval required") {
		t.Errorf("repo.create refused: %v", err)
	}
}

// permissionScopeRig is permissionRig with typed graph refs on and the session
// attached to the role node forge.
func permissionScopeRig(t *testing.T) (*Store, string) {
	t.Helper()
	s, node, session := permissionRig(t)
	if _, err := s.AddProjects(node, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "other", Path: "/tmp/other"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET agent_id='forge' WHERE id=?`, session); err != nil {
		t.Fatal(err)
	}
	return s, session
}

func proposeRules(t *testing.T, s *Store, session string, rules ...string) protocol.PermissionProposeResult {
	t.Helper()
	res, err := s.ProposePermission(protocol.PermissionProposeFrame{SessionID: session,
		PermissionProposeRequest: protocol.PermissionProposeRequest{Rules: rules, Targets: []string{"forge"}, Reason: "merge PRs"}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A proposal from a session whose role node holds the permissions scope over
// the target repo is approved by that scope and opens no Desk ask. A proposal
// with no such scope, a rule on the never list, or a scope over another repo
// still waits for the owner, exactly as before.
func TestPermissionProposalApprovedByScope(t *testing.T) {
	s, session := permissionScopeRig(t)
	asks := func() int {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if res := proposeRules(t, s, session, "Bash(gh pr merge:*)"); res.Status != "pending" || asks() != 1 {
		t.Fatalf("no scope: %+v, %d asks", res, asks())
	}
	if _, err := s.SetScope(scopeSet("forge", protocol.ScopePermissions, "repo:other", ""), "owner", false); err != nil {
		t.Fatal(err)
	}
	before := asks()
	if res := proposeRules(t, s, session, "Bash(gh pr merge:*)"); res.Status != "pending" || asks() != before+1 {
		t.Fatalf("a scope over another repo: %+v", res)
	}
	if _, err := s.SetScope(scopeSet("forge", protocol.ScopePermissions, "repo:forge", ""), "owner", false); err != nil {
		t.Fatal(err)
	}
	before = asks()
	res := proposeRules(t, s, session, "Bash(gh pr merge:*)")
	if res.Status != "approved" || asks() != before {
		t.Fatalf("a scope over the target: %+v, %d asks (was %d)", res, asks(), before)
	}
	p, err := s.PermissionProposal(res.ID)
	if err != nil || p.Status != "approved" || p.DecidedBy != "scope:forge" {
		t.Fatalf("stored proposal %+v %v", p, err)
	}
	for _, rule := range []string{"Bash(git push --force:*)", "Bash", "Bash(rm -rf:*)"} {
		before = asks()
		if res := proposeRules(t, s, session, rule); res.Status != "pending" || asks() != before+1 {
			t.Fatalf("never-list rule %q: %+v", rule, res)
		}
	}
}

// An ask from a repo that a node's asks scope covers goes to a live session of
// that node, with a message, before the chief or the Desk. An ask from a repo
// no asks scope covers is routed as before.
func TestAskGoesToTheAsksScopeHolderFirst(t *testing.T) {
	s := teamStore(t)
	var asker, holder string
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE native_id='n-demo'`).Scan(&asker); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE native_id='n-other'`).Scan(&holder); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET agent_id=CASE id WHEN ? THEN 'demo' ELSE 'other' END, online=1 WHERE id IN (?,?)`, asker, asker, holder); err != nil {
		t.Fatal(err)
	}
	ask := func(text string) protocol.InboxItem {
		t.Helper()
		item, err := s.createInboxItem(s.db, protocol.InboxItem{Kind: "question", SessionID: asker, Node: "demo", Text: text, Options: []string{"yes", "no"}})
		if err != nil {
			t.Fatal(err)
		}
		var next string
		if err := s.db.QueryRow(`SELECT next_who FROM inbox_items WHERE id=?`, item.ID).Scan(&next); err != nil {
			t.Fatal(err)
		}
		item.NextWho = next
		return item
	}
	if got := ask("first question").NextWho; got != protocol.AskNextOwner {
		t.Fatalf("with no scope the ask went to %q", got)
	}
	if _, err := s.SetScope(scopeSet("other", protocol.ScopeAsks, "repo:other", ""), "owner", false); err != nil {
		t.Fatal(err)
	}
	if got := ask("second question").NextWho; got != protocol.AskNextOwner {
		t.Fatalf("a scope over another repo sent the ask to %q", got)
	}
	if _, err := s.SetScope(scopeSet("other", protocol.ScopeAsks, "repo:demo", ""), "owner", false); err != nil {
		t.Fatal(err)
	}
	item := ask("third question")
	if item.NextWho != holder {
		t.Fatalf("the ask went to %q, want the holder %s", item.NextWho, holder)
	}
	var body string
	if err := s.db.QueryRow(`SELECT body FROM messages WHERE recipient=? AND idem_key LIKE ?`, holder, chiefAskKeyPrefix+item.ID+"%").Scan(&body); err != nil {
		t.Fatalf("no message for the holder: %v", err)
	}
	if !strings.Contains(body, "third question") || !strings.Contains(body, "agent_ask_sort") {
		t.Fatalf("message %q", body)
	}
}
