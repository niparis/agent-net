package gateway

import (
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSessionIdentityNewAndWorkerRun(t *testing.T) {
	r := newDeliveryRig(t)
	for _, tc := range []struct{ id, agent, repo string }{{r.caller, "demo", "demo"}, {r.worker, "", "demo"}, {r.sonnet, "demo", "demo"}} {
		var agent, repo string
		if err := r.s.db.QueryRow(`SELECT COALESCE(agent_id,''),COALESCE(repo_id,'') FROM sessions WHERE id=?`, tc.id).Scan(&agent, &repo); err != nil {
			t.Fatal(err)
		}
		if agent != tc.agent || repo != tc.repo {
			t.Fatalf("%s got (%q,%q), want (%q,%q)", tc.id, agent, repo, tc.agent, tc.repo)
		}
	}
	if err := r.s.RecordWorkerRun(protocol.WorkerRun{NodeID: r.a, SessionID: r.worker, Harness: "claude", Outcome: protocol.OutcomeComplete, CallerSessionID: r.caller}); err != nil {
		t.Fatal(err)
	}
	var caller string
	if err := r.s.db.QueryRow(`SELECT COALESCE(caller_agent_id,'') FROM worker_runs WHERE session_id=? ORDER BY id DESC LIMIT 1`, r.worker).Scan(&caller); err != nil || caller != "demo" {
		t.Fatalf("caller agent %q: %v", caller, err)
	}
}

func TestSessionIdentityMigrationBackfill(t *testing.T) {
	r := newDeliveryRig(t)
	cto, _, err := r.s.upsertSession(r.s.db, r.a, protocol.SessionInfo{Harness: "cto", NativeID: "cto"}, true)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.RecordWorkerRun(protocol.WorkerRun{NodeID: r.a, SessionID: r.worker, Harness: "claude", Outcome: protocol.OutcomeComplete, CallerSessionID: r.caller}); err != nil {
		t.Fatal(err)
	}
	other, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "other", Repo: protocol.RepoInfo{Project: "other", Root: "/code/other"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "demo", To: "other", Kind: "depends", Rule: "ask"}, "owner"); err != nil {
		t.Fatal(err)
	}
	snapshot := func() []any {
		t.Helper()
		relation, err := r.s.RelationFor(r.caller, other)
		if err != nil {
			t.Fatal(err)
		}
		rule, err := r.s.RequestRule(r.caller, other)
		if err != nil {
			t.Fatal(err)
		}
		authority, err := r.s.MergeAuthorityForSession(r.sonnet)
		if err != nil {
			t.Fatal(err)
		}
		policy, err := deliveryCall[protocol.PolicyGot](r, r.a, "policy.get", protocol.PolicyGet{SessionID: r.caller})
		if err != nil {
			t.Fatal(err)
		}
		pipeline, err := r.s.deliveryPipeline("demo")
		if err != nil {
			t.Fatal(err)
		}
		group, err := deliveryCall[struct {
			Allowed bool `json:"allowed"`
		}](r, r.a, "group.authority", map[string]string{"session_id": r.caller})
		if err != nil {
			t.Fatal(err)
		}
		return []any{relation, rule, authority, policy, pipeline, group}
	}
	before := snapshot()
	if _, err := r.s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "Explicit session and run identity")); err != nil {
		t.Fatal(err)
	}
	if err := migrateOldFixture(t, r.s); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, agent, repo string }{{r.caller, "demo", "demo"}, {r.worker, "", "demo"}, {r.sonnet, "demo", "demo"}, {cto, "cto", ""}, {owner, "", ""}} {
		var agent, repo string
		if err := r.s.db.QueryRow(`SELECT COALESCE(agent_id,''),COALESCE(repo_id,'') FROM sessions WHERE id=?`, tc.id).Scan(&agent, &repo); err != nil {
			t.Fatal(err)
		}
		if agent != tc.agent || repo != tc.repo {
			t.Fatalf("%s got (%q,%q) want (%q,%q)", tc.id, agent, repo, tc.agent, tc.repo)
		}
	}
	var caller string
	if err := r.s.db.QueryRow(`SELECT COALESCE(caller_agent_id,'') FROM worker_runs WHERE session_id=?`, r.worker).Scan(&caller); err != nil || caller != "demo" {
		t.Fatalf("caller %q: %v", caller, err)
	}
	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("routing or policy changed: before %+v, after %+v", before, after)
	}
}

func TestSessionIdentityUsesSelectedAgentAndRepoSeparately(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET agent_id='advisor' WHERE id=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`INSERT INTO agent_charter VALUES('advisor','purpose','Advisor purpose',1,'owner')`); err != nil {
		t.Fatal(err)
	}
	c, err := r.s.resolveSessionContext(r.caller)
	if err != nil || c.Project != "demo" || c.Agent != "advisor" || c.AgentCharter.Purpose != "Advisor purpose" {
		t.Fatalf("context %+v: %v", c, err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "advisor", To: "demo", Kind: "governs", Mode: "advisor", Rule: "auto"}, "owner"); err != nil {
		t.Fatal(err)
	}
	rule, err := r.s.RequestRule(r.caller, r.sonnet)
	if err != nil || rule != "auto" {
		t.Fatalf("governs rule %q: %v", rule, err)
	}
}

func TestWorkerRunKeepsCallerAgentAtStart(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET agent_id='advisor' WHERE id=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	if err := r.s.TrackWorkerStart("ref", r.a, r.caller, "demo", "claude", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.s.startSession(r.a, "ref", r.worker); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET agent_id='demo' WHERE id=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	c, err := r.s.resolveSessionContext(r.worker)
	if err != nil || c.Agent != "advisor" {
		t.Fatalf("worker context %+v: %v", c, err)
	}
	if err := r.s.RecordWorkerRun(protocol.WorkerRun{NodeID: r.a, SessionID: r.worker, Harness: "claude", Outcome: protocol.OutcomeComplete, CallerSessionID: r.caller}); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := r.s.db.QueryRow(`SELECT id FROM worker_runs WHERE session_id=?`, r.worker).Scan(&id); err != nil {
		t.Fatal(err)
	}
	c, err = r.s.resolveRunContext(id)
	if err != nil || c.Agent != "advisor" {
		t.Fatalf("run context %+v: %v", c, err)
	}
}

func TestSessionIdentityGroupAuthority(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "advisor", To: "demo", Kind: "governs", Mode: "advisor"}, "owner"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		Allowed bool `json:"allowed"`
	}
	allowed, err := deliveryCall[result](r, r.a, "group.authority", map[string]string{"session_id": r.caller})
	if err != nil || allowed.Allowed {
		t.Fatalf("ordinary session %+v: %v", allowed, err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET agent_id='advisor' WHERE id=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	allowed, err = deliveryCall[result](r, r.a, "group.authority", map[string]string{"session_id": r.caller})
	if err != nil || !allowed.Allowed {
		t.Fatalf("advisor session %+v: %v", allowed, err)
	}
	if _, err = deliveryCall[result](r, r.b, "group.authority", map[string]string{"session_id": r.caller}); err == nil {
		t.Fatal("another machine got authority")
	}
}

func TestWorkerRunKeepsEmptyCallerAgentAtStart(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.db.Exec(`UPDATE sessions SET agent_id=NULL WHERE id=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	if err := r.s.TrackWorkerStart("ref-empty", r.a, r.caller, "demo", "claude", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.s.startSession(r.a, "ref-empty", r.worker); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET agent_id='demo' WHERE id=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	c, err := r.s.resolveSessionContext(r.worker)
	if err != nil || c.Agent != "" {
		t.Fatalf("worker context %+v: %v", c, err)
	}
	if err := r.s.RecordWorkerRun(protocol.WorkerRun{NodeID: r.a, SessionID: r.worker, Harness: "claude", Outcome: protocol.OutcomeComplete, CallerSessionID: r.caller}); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := r.s.db.QueryRow(`SELECT id FROM worker_runs WHERE session_id=?`, r.worker).Scan(&id); err != nil {
		t.Fatal(err)
	}
	c, err = r.s.resolveRunContext(id)
	if err != nil || c.Agent != "" {
		t.Fatalf("run context %+v: %v", c, err)
	}
}

func TestSessionIdentityRepoDrivesMergerAndPolicy(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.AddProjects(r.a, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "other", Path: "/code/other"}}}); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetCharter("other", "accepts", "Other work", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "pi", NativeID: "other-context", Repo: protocol.RepoInfo{Project: "other", Root: "/code/other"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET repo_id='other' WHERE id=?`, r.sonnet); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "other", NodeID: r.a, SessionID: r.sonnet}, "owner"); err != nil {
		t.Fatalf("context merger rejected: %v", err)
	}
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet}, "owner"); err == nil {
		t.Fatal("stale project accepted for merger")
	}
	got, err := deliveryCall[protocol.PolicyGot](r, r.a, "context.get", protocol.PolicyGet{SessionID: r.sonnet})
	if err != nil || !strings.Contains(got.Charter, "Other work") {
		t.Fatalf("context %q: %v", got.Charter, err)
	}
}

func TestDeliveryClaimUsesSessionRepoIdentity(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.AddProjects(r.a, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "other", Path: "/code/other"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET repo_id='other' WHERE id=?`, r.sonnet); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`INSERT INTO delivery_step_tasks(change_id,revision,step,owner_node,created_at) VALUES('change',1,'test','other',1)`); err != nil {
		t.Fatal(err)
	}
	tx, err := r.s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	found, err := claimNodeTask(tx, protocol.Change{ID: "change", Revision: 1}, protocol.StepRef{Step: "test"}, r.sonnet, r.a, false)
	if err != nil || !found {
		t.Fatalf("claim found=%v err=%v", found, err)
	}
}

func TestSessionIdentityRepoDrivesAgentCharter(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.AddProjects(r.a, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "other", Path: "/code/other"}}}); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetCharter("other", "accepts", "Other work", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET repo_id='other' WHERE id=?`, r.sonnet); err != nil {
		t.Fatal(err)
	}
	a, ok, err := r.s.Agent(r.sonnet)
	if err != nil || !ok || a.Charter.Accepts != "Other work" {
		t.Fatalf("charter %+v ok=%v err=%v", a.Charter, ok, err)
	}
}

func TestWorkerRunUsesLatestStartWithSameTimestamp(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET agent_id='advisor' WHERE id=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	if err := r.s.TrackWorkerStart("first", r.a, r.caller, "demo", "claude", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.s.startSession(r.a, "first", r.worker); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET agent_id='demo' WHERE id=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	if err := r.s.TrackWorkerStart("second", r.a, r.caller, "demo", "claude", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.s.startSession(r.a, "second", r.worker); err != nil {
		t.Fatal(err)
	}
	c, err := r.s.resolveSessionContext(r.worker)
	if err != nil || c.Agent != "demo" {
		t.Fatalf("latest worker context %+v: %v", c, err)
	}
	if err := r.s.RecordWorkerRun(protocol.WorkerRun{NodeID: r.a, SessionID: r.worker, Harness: "claude", Outcome: protocol.OutcomeComplete, CallerSessionID: r.caller}); err != nil {
		t.Fatal(err)
	}
	var agent string
	if err := r.s.db.QueryRow(`SELECT COALESCE(caller_agent_id,'') FROM worker_runs WHERE session_id=?`, r.worker).Scan(&agent); err != nil || agent != "demo" {
		t.Fatalf("latest run agent %q: %v", agent, err)
	}
}

func TestSessionIdentityRepoDrivesSessionFind(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.AddProjects(r.a, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "other", Path: "/code/other"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET repo_id='other' WHERE id=?`, r.sonnet); err != nil {
		t.Fatal(err)
	}
	page, err := r.s.FindSessions(protocol.SessionsFindRequest{Project: "other", Online: "any"})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Items {
		if row.ID == r.sonnet && row.Project == "other" {
			return
		}
	}
	t.Fatalf("missing session in repo context: %+v", page.Items)
}

func TestSessionFindKeepsLegacyAnnouncedProjectDisplay(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.db.Exec(`INSERT INTO profiles(session_id,name,function,project,capabilities,task,updated_at,expertise) VALUES(?,'','','announced','[]','',1,'') ON CONFLICT(session_id) DO UPDATE SET project='announced'`, r.sonnet); err != nil {
		t.Fatal(err)
	}
	page, err := r.s.FindSessions(protocol.SessionsFindRequest{Project: "announced", Online: "any"})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Items {
		if row.ID == r.sonnet && row.Project == "announced" {
			return
		}
	}
	t.Fatalf("legacy announced project changed: %+v", page.Items)
}

func TestRequestRuleAndStorageUseResolvedFallbackAgent(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.AddProjects(r.a, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "other", Path: "/code/other"}}}); err != nil {
		t.Fatal(err)
	}
	if err := r.s.AddArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET agent_id='advisor' WHERE id=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET repo_id='other',agent_id=NULL WHERE id=?`, r.sonnet); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "advisor", To: "other", Kind: "governs", Mode: "advisor", Rule: "ask"}, "owner"); err != nil {
		t.Fatal(err)
	}
	rule, err := r.s.RequestRule(r.caller, r.sonnet)
	if err != nil || rule != "ask" {
		t.Fatalf("rule %q: %v", rule, err)
	}
	m, _, err := send(r.s, r.caller, r.sonnet, "request", "")
	if err != nil || m.State != protocol.StateHeld {
		t.Fatalf("message state %q: %v", m.State, err)
	}
}

func TestSessionIdentityMigrationRejectsExistingColumns(t *testing.T) {
	s, _ := newTestStore(t)
	version := migrationVersion(t, "Explicit session and run identity")
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, version); err != nil {
		t.Fatal(err)
	}
	path := s.path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(path)
	if err == nil {
		reopened.Close()
		t.Fatal("migration silently accepted an inconsistent schema")
	}
	if !strings.Contains(err.Error(), "duplicate column") {
		t.Fatalf("unexpected migration error: %v", err)
	}
}
