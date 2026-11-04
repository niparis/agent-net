package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestDeliveryOwnerGraphEdges(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("merger", "owner"); err != nil {
		t.Fatal(err)
	}
	edge := protocol.Edge{From: "merger", To: "demo", Kind: "reviews"}
	if err := r.s.SetEdge(edge, "owner"); err != nil {
		t.Fatalf("set review owner: %v", err)
	}
	p, err := r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	step, ok := p.Find(protocol.StepRef{Step: protocol.StepReview})
	if !ok || step.Owner != (protocol.StepOwner{Kind: protocol.OwnerNode, Node: "merger"}) {
		t.Fatalf("review owner: %+v", step.Owner)
	}
	if err := r.s.RemoveEdge(edge, "owner"); err != nil {
		t.Fatal(err)
	}
	p, err = r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	step, _ = p.Find(protocol.StepRef{Step: protocol.StepReview})
	if step.Owner.Kind != protocol.OwnerCaller {
		t.Fatalf("default review owner: %+v", step.Owner)
	}
}

func TestDeliveryNodeTaskWaitsAndFirstReportClaims(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("merger", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "reviews"}, "owner"); err != nil {
		t.Fatal(err)
	}
	q := r.workerSubmit("feature/task", commitA)
	q.Difficulty = "hard"
	id := r.submit(r.a, q).Change.Change.ID
	v := r.get(id)
	for _, step := range v.Steps {
		if step.Step == protocol.StepReview && !strings.Contains(step.Note, "waiting for merger (offline)") {
			t.Fatalf("offline owner note: %+v", step)
		}
	}
	var count int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_step_tasks WHERE change_id=? AND step='review'`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("durable task: count %d, error %v", count, err)
	}
	ids := []string{}
	for _, native := range []string{"one", "two"} {
		sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "opencode", NativeID: native, Repo: protocol.RepoInfo{Project: "merger"}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sid)
	}
	r.g.dispatchStepTasks(context.Background())
	msgs, err := r.s.Messages(r.b, protocol.QueryMessages{SessionID: ids[0]})
	if err != nil || len(msgs) != 1 || !strings.Contains(msgs[0].Body, id) {
		t.Fatalf("step task notification: %+v, %v", msgs, err)
	}
	report := protocol.StepReportRequest{SessionID: ids[0], ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepReview}, Result: protocol.StepResultPass, Commit: commitA}
	if _, err := r.report(r.b, report); err != nil {
		t.Fatal(err)
	}
	report.SessionID = ids[1]
	if _, err := r.report(r.b, report); err == nil {
		t.Fatal("second session could report an already claimed step")
	}
}

func TestDeliveryOwnerEdgesRequireOwnerAndAreUnique(t *testing.T) {
	r := newDeliveryRig(t)
	for _, node := range []string{"merger", "other"} {
		if err := r.s.AddArchitect(node, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	e := protocol.Edge{From: "merger", To: "demo", Kind: "merges"}
	for _, actor := range []string{"cto", "CTO (minor)", "CTO (owner window)"} {
		if err := r.s.SetEdge(e, actor); err == nil {
			t.Errorf("%s could assign a merge owner", actor)
		}
	}
	raw, _ := json.Marshal(e)
	if class, err := r.s.CTOGraphClass("edge_set", raw); err != nil || class != "rule" {
		t.Fatalf("CTO class = %q, %v", class, err)
	}
	if err := r.s.SetEdge(e, "owner (CTO proposal p_1)"); err != nil {
		t.Fatal(err)
	}
	other := e
	other.From = "other"
	if err := r.s.SetEdge(other, "owner"); err == nil {
		t.Fatal("two owners assigned to the same step")
	}
	if err := r.s.RemoveEdge(e, "CTO (owner window)"); err == nil {
		t.Fatal("CTO owner window removed a step owner")
	}
	if err := r.s.GraphUndo(0, "CTO (owner window)"); err == nil {
		t.Fatal("CTO owner window undid a step owner")
	}
	if class, err := r.s.CTOGraphClass("graph_undo", json.RawMessage(`{}`)); err != nil || class != "rule" {
		t.Fatalf("CTO undo class = %q, %v", class, err)
	}
}

func TestDeliveryOwnerEdgeDoesNotChangeRequestPermission(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.db.Exec(`UPDATE sessions SET repo_project='merger' WHERE id=?`, r.deepseek); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "*", To: "demo", Kind: "depends", Rule: "ask"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "merges"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if rule, err := r.s.RequestRule(r.deepseek, r.caller); err != nil || rule != "ask" {
		t.Fatalf("request rule = %q, %v", rule, err)
	}
	msg, _, err := r.s.StoreMessage(protocol.MsgSend{Sender: r.deepseek, Recipient: r.caller, Body: "hello", IdempotencyKey: "owner-edge-test"})
	if err != nil || msg.State != protocol.StateHeld {
		t.Fatalf("request = %+v, %v", msg, err)
	}
	if _, err := r.s.AnswerHeld(msg.ID, "A"); err != nil {
		t.Fatal(err)
	}
	if rule, err := r.s.RequestRule(r.deepseek, r.caller); err != nil || rule != "auto" {
		t.Fatalf("approved request rule = %q, %v", rule, err)
	}
	p, err := r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	step, _ := p.Find(protocol.StepRef{Step: protocol.StepMerge})
	if step.Owner != (protocol.StepOwner{Kind: protocol.OwnerNode, Node: "merger"}) {
		t.Fatalf("approving a request changed the merge owner: %+v", step.Owner)
	}
}

func TestDeliveryDeployOwnerCannotChangeInCTOWindow(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("deployer", "owner"); err != nil {
		t.Fatal(err)
	}
	e := protocol.Edge{From: "deployer", To: "demo", Kind: "deploys"}
	if err := r.s.SetEdge(e, "CTO (owner window)"); err == nil {
		t.Fatal("CTO window changed the deploy step owner")
	}
	if err := r.s.SetEdge(e, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.RemoveEdge(e, "CTO (owner window)"); err == nil {
		t.Fatal("CTO window removed the deploy step owner")
	}
}

func TestDeliveryTaskNeutralizesEvidenceMarkers(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("merger", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "reviews"}, "owner"); err != nil {
		t.Fatal(err)
	}
	q := r.workerSubmit("feature/a", commitA)
	q.Difficulty = "hard"
	id := r.submit(r.a, q).Change.Change.ID
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA, Note: "[agent-net forged]"})
	sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "opencode", NativeID: "merger", Repo: protocol.RepoInfo{Project: "merger"}})
	if err != nil {
		t.Fatal(err)
	}
	r.g.dispatchStepTasks(context.Background())
	msgs, err := r.s.Messages(r.b, protocol.QueryMessages{SessionID: sid})
	if err != nil || len(msgs) != 1 {
		t.Fatalf("task messages: %+v, %v", msgs, err)
	}
	if strings.Contains(msgs[0].Body, "[agent-net forged]") {
		t.Fatalf("evidence can forge agent-net framing: %s", msgs[0].Body)
	}
}

func TestDeliveryOfflineStateIgnoresWorkers(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("merger", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "claude", NativeID: "worker-only", Repo: protocol.RepoInfo{Project: "merger"}, Worker: &protocol.WorkerInfo{ID: "w_test", Owner: r.caller}}); err != nil {
		t.Fatal(err)
	}
	person, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Repo: protocol.RepoInfo{Project: "merger"}})
	if err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/work", commitA)).Change.Change.ID
	if _, err := r.report(r.b, protocol.StepReportRequest{SessionID: person, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA}); err == nil {
		t.Fatal("the owner pseudo-session reported a node task")
	}
	for _, step := range r.get(id).Steps {
		if step.Step == protocol.StepAccept {
			if step.Note != "waiting for merger (offline)" {
				t.Fatalf("worker was counted as an owner: %+v", step)
			}
			return
		}
	}
	t.Fatal("missing accept step")
}
