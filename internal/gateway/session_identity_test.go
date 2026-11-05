package gateway

import (
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
	if _, err := r.s.db.Exec(`DROP TRIGGER session_identity_repo_update; ALTER TABLE sessions DROP COLUMN agent_id; ALTER TABLE sessions DROP COLUMN repo_id; ALTER TABLE worker_runs DROP COLUMN caller_agent_id; ALTER TABLE worker_starts DROP COLUMN caller_agent_id`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(migrations[len(migrations)-1]); err != nil {
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
