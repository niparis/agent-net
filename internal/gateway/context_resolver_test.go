package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestResolverAgentCharterAndWorker(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.SetCharter("demo", "purpose", "Repo purpose", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`INSERT INTO agent_charter VALUES('demo','purpose','Agent goal',1,'owner')`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.AddProjects(r.a, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "other", Path: "/code/other"}}}); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetCharter("other", "purpose", "Other repo purpose", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET repo_project='other' WHERE id=?`, r.worker); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id     string
		worker bool
	}{{r.caller, false}, {r.worker, true}} {
		c, err := r.s.resolveSessionContext(tc.id)
		if err != nil {
			t.Fatal(err)
		}
		wantRepo := "Repo purpose"
		if tc.worker {
			wantRepo = "Other repo purpose"
		}
		if c.RepoCharter.Purpose != wantRepo || c.AgentCharter.Purpose != "Agent goal" || c.CallerContext != tc.worker {
			t.Fatalf("context %+v", c)
		}
		if !strings.Contains(c.agentText(), "Agent goal") {
			t.Fatalf("agent text %q", c.agentText())
		}
	}
	for _, harness := range []string{"cto", "owner"} {
		id, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "pseudo-" + harness, Repo: protocol.RepoInfo{Project: "demo"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.s.db.Exec(`UPDATE sessions SET harness=? WHERE id=?`, harness, id); err != nil {
			t.Fatal(err)
		}
		c, err := r.s.resolveSessionContext(id)
		if err != nil {
			t.Fatal(err)
		}
		if c.agentText() != "" {
			t.Fatalf("%s: %q", harness, c.agentText())
		}
	}
}

func TestResolverPolicyDeliveryCompatibility(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.SetCharter("demo", "purpose", "Repo purpose", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: []byte(`"ask"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet, Configure: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	a, ok, err := r.s.Agent(r.caller)
	if err != nil || !ok {
		t.Fatalf("agent: %v", err)
	}
	want := protocol.PolicyGot{Harness: a.Harness, Delegation: a.Delegation, Project: projectOfSession(a)}
	got, err := deliveryCall[protocol.PolicyGot](r, r.a, "policy.get", protocol.PolicyGet{SessionID: r.caller})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(want)
	after, _ := json.Marshal(got)
	if string(before) != string(after) {
		t.Fatalf("policy changed: %s != %s", before, after)
	}
	graph, err := r.s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	snap, err := r.s.SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	legacyGraph, err := r.s.GraphFor("demo")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := r.s.resolveSessionContext(r.caller)
	if err != nil {
		t.Fatal(err)
	}
	legacyText := protocol.NodeContextWithSettings(legacyGraph, "demo", snap)
	resolvedText := protocol.NodeContextWithSettings(resolved.Graph, resolved.Repo, resolved.Settings)
	if legacyText != resolvedText {
		t.Fatalf("charter context changed:\n%q\n%q", legacyText, resolvedText)
	}
	gotContext, err := deliveryCall[protocol.PolicyGot](r, r.a, "context.get", protocol.PolicyGet{SessionID: r.caller})
	if err != nil || gotContext.Charter != legacyText {
		t.Fatalf("context.get changed: %q != %q: %v", gotContext.Charter, legacyText, err)
	}
	oldPipeline, err := protocol.PipelineFromSettings(snap, chartedProject(graph, "demo"))
	if err != nil {
		t.Fatal(err)
	}
	newPipeline, err := r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	before, _ = json.Marshal(oldPipeline)
	after, _ = json.Marshal(newPipeline)
	if string(before) != string(after) {
		t.Fatalf("pipeline changed: %s != %s", before, after)
	}
	if _, err := r.s.db.Exec(`INSERT INTO agent_charter VALUES('demo','purpose','Agent goal',1,'owner')`); err != nil {
		t.Fatal(err)
	}
	got, err = deliveryCall[protocol.PolicyGot](r, r.a, "policy.get", protocol.PolicyGet{SessionID: r.caller})
	if err != nil || !strings.Contains(got.Charter, "Agent goal") {
		t.Fatalf("policy %+v: %v", got, err)
	}
}

func TestResolverWorkerRun(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.db.Exec(`INSERT INTO agent_charter VALUES('demo','purpose','caller goal',1,'owner')`); err != nil {
		t.Fatal(err)
	}
	res, err := r.s.db.Exec(`INSERT INTO worker_runs(node_id,session_id,harness,outcome,ended_at,caller_session_id) VALUES(?,?,'claude','complete',1,?)`, r.a, r.worker, r.caller)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.s.resolveRunContext(id)
	if err != nil {
		t.Fatal(err)
	}
	if c.AgentCharter.Purpose != "caller goal" || !c.CallerContext {
		t.Fatalf("run context: %+v", c)
	}
}
