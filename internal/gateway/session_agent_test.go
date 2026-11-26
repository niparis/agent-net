package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSessionAgentOwnerAssignment(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`INSERT INTO charter_values VALUES('agent','advisor','purpose','Advisor goal',1,'owner')`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES('workers.auto_continue','agent','advisor','','false',1)`); err != nil {
		t.Fatal(err)
	}
	owner, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.caller}, "owner"); err != nil {
		t.Fatal(err)
	}
	request := map[string]string{"session_id": r.caller, "agent": "advisor", "owner_session": owner}
	if _, err = deliveryCall[map[string]any](r, r.b, "session.agent.set", request); err == nil {
		t.Fatal("another node used owner identity")
	}
	request["owner_session"] = r.caller
	if _, err = deliveryCall[map[string]any](r, r.a, "session.agent.set", request); err == nil {
		t.Fatal("agent changed its own identity")
	}
	request["owner_session"] = owner
	if _, err = deliveryCall[map[string]any](r, r.a, "session.agent.set", request); err != nil {
		t.Fatal(err)
	}
	c, err := r.s.resolveSessionContext(r.caller)
	if err != nil || c.Agent != "advisor" || c.AgentCharter.Purpose != "Advisor goal" {
		t.Fatalf("context %+v: %v", c, err)
	}
	effective, err := c.resolveSetting("workers.auto_continue", nil, nil)
	if err != nil || string(effective.Value) != "false" || effective.Source != "agent advisor" {
		t.Fatalf("agent setting %+v: %v", effective, err)
	}
	// A pre-3d daemon sends only the original session fields on every sync.
	if _, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "caller", Repo: protocol.RepoInfo{Project: "demo", Root: "/code/demo"}}); err != nil {
		t.Fatal(err)
	}
	view, ok, err := r.s.Agent(r.caller)
	if err != nil || !ok || view.AgentID != "advisor" || view.AgentName != "advisor" {
		t.Fatalf("old sync lost assignment: %+v %v", view, err)
	}
	options, err := deliveryCall[protocol.SessionAgentOptions](r, r.a, "session.agent.list", map[string]string{"owner_session": owner})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, option := range options.Agents {
		if option.ID == "advisor" && option.Name == "advisor" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing advisor: %+v", options)
	}
	if _, err = deliveryCall[protocol.SessionAgentOptions](r, r.a, "session.agent.list", map[string]string{"owner_session": r.caller}); err == nil {
		t.Fatal("agent listed owner choices")
	}
	var merger string
	if err := r.s.db.QueryRow(`SELECT session_id FROM `+mergerRowsSQL+` WHERE project='demo' AND node_id=?`, r.a).Scan(&merger); err != nil || merger != r.caller {
		t.Fatalf("merger changed %q: %v", merger, err)
	}
	cto, _, err := r.s.upsertSession(r.s.db, r.a, protocol.SessionInfo{Harness: "cto", NativeID: "cto"}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{r.worker, owner, cto} {
		request["session_id"] = id
		if _, err = deliveryCall[map[string]any](r, r.a, "session.agent.set", request); err == nil {
			t.Fatalf("assigned %s", id)
		}
	}
	request["session_id"], request["agent"] = r.caller, "missing"
	if _, err = deliveryCall[map[string]any](r, r.a, "session.agent.set", request); err == nil {
		t.Fatal("unknown agent accepted")
	}
}

func TestSessionAgentAssignmentSurvivesOldDaemonSyncForEveryHarness(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	owner, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"claude", "codex", "opencode", "pi"} {
		t.Run(harness, func(t *testing.T) {
			info := protocol.SessionInfo{Harness: harness, NativeID: "old-" + harness, Repo: protocol.RepoInfo{Project: "demo", Root: "/code/demo"}}
			id, err := r.s.UpsertSession(r.a, info)
			if err != nil {
				t.Fatal(err)
			}
			// demo has a live lead session, so a new session joins as the
			// project's member Agent (decision 0193 I2).
			initial, err := r.s.resolveSessionContext(id)
			if err != nil || initial.Agent != "demo-agent" {
				t.Fatalf("default: %+v %v", initial, err)
			}
			req := map[string]string{"session_id": id, "agent": "advisor", "owner_session": owner}
			if _, err = deliveryCall[map[string]any](r, r.a, "session.agent.set", req); err != nil {
				t.Fatal(err)
			}
			if _, err = deliveryCall[map[string]any](r, r.a, "session.agent.set", req); err != nil {
				t.Fatal(err)
			}
			// Old daemons sync the complete set without any identity fields.
			if _, err = r.s.SyncSessions(r.a, []protocol.SessionInfo{info, {Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID}}); err != nil {
				t.Fatal(err)
			}
			after, err := r.s.resolveSessionContext(id)
			if err != nil || after.Agent != "advisor" || after.Project != "demo" {
				t.Fatalf("after old sync: %+v %v", after, err)
			}
		})
	}
}

func TestSessionAgentOwnerCanAssignRemoteSession(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	owner, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	req := map[string]string{"session_id": r.caller, "agent": "advisor", "owner_session": owner}
	if _, err := deliveryCall[map[string]any](r, r.b, "session.agent.set", req); err != nil {
		t.Fatal(err)
	}
	ctx, err := r.s.resolveSessionContext(r.caller)
	if err != nil || ctx.Agent != "advisor" {
		t.Fatalf("remote assignment %+v: %v", ctx, err)
	}
}

func TestSessionAgentHeldDecisionKeepsLegacyRepoRule(t *testing.T) {
	r := newDeliveryRig(t)
	other, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "provider", Repo: protocol.RepoInfo{Project: "provider", Root: "/code/provider"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.s.SetEdge(protocol.Edge{From: "demo", To: "provider", Kind: "depends", Rule: "ask"}, "owner"); err != nil {
		t.Fatal(err)
	}
	m, _, err := send(r.s, r.caller, other, "request", "")
	if err != nil || m.State != protocol.StateHeld {
		t.Fatalf("held state %q: %v", m.State, err)
	}
	if _, err = r.s.AnswerHeld(m.ID, "R"); err != nil {
		t.Fatal(err)
	}
	rule, err := r.s.RequestRule(r.caller, other)
	if err != nil || rule != "never" {
		t.Fatalf("rule %q: %v", rule, err)
	}
}
