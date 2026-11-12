package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// listCall sends one list frame the way the daemon does and decodes the answer.
func etagCall[T any](t *testing.T, g *Gateway, node, typ string, req any) T {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := g.handle(context.Background(), &nodeConn{id: node}, typ, raw)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(data)
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A heartbeat moves the sessions table and last_seen of an online session by
// seconds. The rows the TUI draws do not change, so the list is NotModified by
// ETag, though its topic version moved. A change the TUI draws gives rows.
func TestAgentsETagSurvivesHeartbeatsButNotRealChanges(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.db.Exec(`UPDATE sessions SET online=1, last_seen=?, status='idle'`, int64(6000*60+10)*1e9); err != nil {
		t.Fatal(err)
	}
	first := etagCall[protocol.AgentsResult](t, r.g, r.a, "query.agents", protocol.QueryAgents{Lite: true, WantETag: true})
	if len(first.Agents) == 0 || first.ETag == "" {
		t.Fatalf("first answer: %d rows, etag %q", len(first.Agents), first.ETag)
	}
	version := r.g.topicVersions()[protocol.TopicAgents]
	// Two sessions report inside the minute, and the second now reported last:
	// the store lists it first, but the rows the TUI gets are in the same order.
	lo, hi := r.caller, r.worker
	if lo > hi {
		lo, hi = hi, lo
	}
	for _, id := range []string{lo, hi} {
		if _, err := r.s.db.Exec(`UPDATE sessions SET last_seen=? WHERE id=?`, int64(6000*60+20)*1e9, id); err != nil {
			t.Fatal(err)
		}
	}
	first = etagCall[protocol.AgentsResult](t, r.g, r.a, "query.agents", protocol.QueryAgents{Lite: true, WantETag: true})
	version = r.g.topicVersions()[protocol.TopicAgents]
	if _, err := r.s.db.Exec(`UPDATE sessions SET last_seen=? WHERE id=?`, int64(6000*60+40)*1e9, hi); err != nil {
		t.Fatal(err)
	}
	if r.g.topicVersions()[protocol.TopicAgents] == version {
		t.Fatal("the heartbeat did not move the agents topic: the test proves nothing")
	}
	same := etagCall[protocol.AgentsResult](t, r.g, r.a, "query.agents", protocol.QueryAgents{Lite: true, IfETag: first.ETag, IfVersion: version})
	if !same.NotModified || len(same.Agents) != 0 || same.ETag != first.ETag {
		t.Fatalf("heartbeat answer: not_modified=%v rows=%d etag=%q, want an unchanged answer", same.NotModified, len(same.Agents), same.ETag)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET status='busy' WHERE id=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	changed := etagCall[protocol.AgentsResult](t, r.g, r.a, "query.agents", protocol.QueryAgents{Lite: true, IfETag: first.ETag})
	if changed.NotModified || len(changed.Agents) != len(first.Agents) || changed.ETag == first.ETag {
		t.Fatalf("status answer: not_modified=%v rows=%d etag=%q, want new rows", changed.NotModified, len(changed.Agents), changed.ETag)
	}
	// A caller that does not ask for an ETag gets none, so other readers see
	// the same answer as before.
	plain := etagCall[protocol.AgentsResult](t, r.g, r.a, "query.agents", protocol.QueryAgents{Lite: true})
	if plain.ETag != "" || plain.NotModified {
		t.Fatalf("plain answer has etag %q not_modified %v", plain.ETag, plain.NotModified)
	}
}

// The lite rows keep the difficulty line of a worker and leave out the judge's
// long text, which only the worker log shows.
func TestLiteAgentsDropWorkerAssessmentText(t *testing.T) {
	full := &protocol.WorkerAssessment{Reasoning: 3, D: 3.4, Label: "hard", PickerLabel: "hard", Reasons: map[string]string{"scope": "long text"},
		PickerNote: "long note", JudgeModel: "m", JudgeHarness: "h", RulesVersion: "v", Bottleneck: "b"}
	agents := []protocol.AgentView{{SessionID: "s_1", Worker: &protocol.WorkerInfo{Assessment: full}}}
	liteAgentViews(agents)
	got := agents[0].Worker.Assessment
	if got.Reasons != nil || got.PickerNote != "" || got.JudgeModel != "" || got.D != 3.4 || got.PickerLabel != "hard" || got.Reasoning != 3 {
		t.Fatalf("lite assessment %+v", got)
	}
	if full.Reasons == nil || full.PickerNote == "" {
		t.Fatal("lite changed the stored assessment")
	}
}

func TestNodesETagAndBrief(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.db.Exec(`UPDATE nodes SET seen_at=1000000000`); err != nil {
		t.Fatal(err)
	}
	first := etagCall[protocol.NodesInfo](t, r.g, r.a, "query.nodes", protocol.QueryNodes{WantETag: true})
	if len(first.Nodes) != 2 || first.ETag == "" {
		t.Fatalf("first answer: %d nodes, etag %q", len(first.Nodes), first.ETag)
	}
	same := etagCall[protocol.NodesInfo](t, r.g, r.a, "query.nodes", protocol.QueryNodes{IfETag: first.ETag})
	if !same.NotModified || len(same.Nodes) != 0 {
		t.Fatalf("unchanged answer: not_modified=%v nodes=%d", same.NotModified, len(same.Nodes))
	}
	if _, err := r.s.db.Exec(`UPDATE nodes SET hostname='renamed' WHERE id=?`, r.a); err != nil {
		t.Fatal(err)
	}
	changed := etagCall[protocol.NodesInfo](t, r.g, r.a, "query.nodes", protocol.QueryNodes{IfETag: first.ETag})
	if changed.NotModified || len(changed.Nodes) != 2 {
		t.Fatalf("renamed answer: not_modified=%v nodes=%d", changed.NotModified, len(changed.Nodes))
	}
	brief := etagCall[protocol.NodesInfo](t, r.g, r.a, "query.nodes", protocol.QueryNodes{Brief: true})
	full, _ := json.Marshal(changed)
	short, _ := json.Marshal(brief)
	if len(brief.Nodes) != 2 || brief.Nodes[0].NodeID == "" || len(short) >= len(full) {
		t.Fatalf("brief answer: %d nodes, %d bytes, full %d bytes", len(brief.Nodes), len(short), len(full))
	}
	if brief.Playbook != nil || brief.Nodes[0].Tracks != nil {
		t.Fatal("brief answer carries the quality tables")
	}
}

// A message or an event moves the changes topic, because the TUI feed rides on
// it, but it does not change the change list. The list is NotModified by ETag.
func TestChangeListETagSurvivesMessagesButNotChanges(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("b1", commitA)).Change.Change.ID
	q := protocol.ChangeListRequest{Lite: true, WantETag: true}
	first := r.list(q)
	if len(first.Changes) != 1 || first.ETag == "" {
		t.Fatalf("first answer: %d changes, etag %q", len(first.Changes), first.ETag)
	}
	version := r.s.readStamp(topicTables[protocol.TopicChanges])
	r.s.Event("msg.stored", r.a, "", "", "noise")
	if r.s.readStamp(topicTables[protocol.TopicChanges]) == version {
		t.Fatal("an event did not move the changes topic: the test proves nothing")
	}
	same := r.list(protocol.ChangeListRequest{Lite: true, IfETag: first.ETag, IfVersion: version})
	if !same.NotModified || len(same.Changes) != 0 {
		t.Fatalf("event answer: not_modified=%v changes=%d", same.NotModified, len(same.Changes))
	}
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	changed := r.list(protocol.ChangeListRequest{Lite: true, IfETag: first.ETag})
	if changed.NotModified || len(changed.Changes) != 1 || changed.ETag == first.ETag {
		t.Fatalf("step answer: not_modified=%v changes=%d etag=%q", changed.NotModified, len(changed.Changes), changed.ETag)
	}
}
