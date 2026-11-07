package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
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

func TestDeliveryOwnerAssignmentReplacesEdgeAtomically(t *testing.T) {
	r := newDeliveryRig(t)
	for _, node := range []string{"first", "second"} {
		if err := r.s.AddArchitect(node, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	q := protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "accept", Node: "first"}
	if err := r.s.SetDeliveryOwner(q, "cto"); err != nil {
		t.Fatal(err)
	}
	q.Node, q.Expect = "missing", "first"
	if err := r.s.SetDeliveryOwner(q, "owner"); err == nil {
		t.Fatal("accepted an unknown owner")
	}
	p, err := r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	step, _ := p.Find(stepAccept)
	if step.Owner.Node != "first" {
		t.Fatalf("failed replacement lost owner: %+v", step.Owner)
	}
	q.Node = "second"
	if err := r.s.SetDeliveryOwner(q, "owner"); err != nil {
		t.Fatal(err)
	}
	q.Node = ""
	if err := r.s.SetDeliveryOwner(q, "owner"); err == nil {
		t.Fatal("stale expected owner was accepted")
	}
	q.Expect = "second"
	if err := r.s.SetDeliveryOwner(q, "owner"); err != nil {
		t.Fatal(err)
	}
	p, err = r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	step, _ = p.Find(stepAccept)
	if step.Owner.Kind != protocol.OwnerCaller {
		t.Fatalf("cleared owner: %+v", step.Owner)
	}
}

func TestDeliveryAssignmentQueuesAlreadyReadyStep(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/ready", commitA)).Change.Change.ID
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "accept", Node: "reviewer"}, "owner"); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := r.s.db.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND step='accept' AND completed=0`, id).Scan(&owner); err != nil || owner != "reviewer" {
		t.Fatalf("ready step owner = %q, %v", owner, err)
	}
}

func TestDeliveryDeployAssignmentPreservesRequestEdges(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("deployer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "deployer", To: "demo", Kind: "deploys", Rule: "ask"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.AddArchitect("other", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET repo_project='other' WHERE id=?`, r.deepseek); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "*", To: "demo", Kind: "depends", Rule: "never"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.deploy_rule", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	q := protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "deploy", Node: "other"}
	if err := r.s.SetDeliveryOwner(q, "owner"); err != nil {
		t.Fatal(err)
	}
	p, err := r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	step, _ := p.Find(protocol.StepRef{Step: protocol.StepDeploy, AfterMerge: true})
	if step.Owner.Node != "other" {
		t.Fatalf("explicit deploy owner: %+v", step.Owner)
	}
	if rule, err := r.s.RequestRule(r.deepseek, r.caller); err != nil || rule != "never" {
		t.Fatalf("assignment changed request permission to %q: %v", rule, err)
	}
	var rule string
	if err := r.s.db.QueryRow(`SELECT rule FROM graph_edges WHERE source='deployer' AND target='demo' AND kind='deploys'`).Scan(&rule); err != nil || rule != "ask" {
		t.Fatalf("legacy deploy rule = %q, %v", rule, err)
	}
	q.Node, q.Expect = "", "other"
	if err := r.s.SetDeliveryOwner(q, "owner"); err != nil {
		t.Fatal(err)
	}
	p, err = r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	step, _ = p.Find(protocol.StepRef{Step: protocol.StepDeploy, AfterMerge: true})
	if step.Owner.Node != "deployer" {
		t.Fatalf("default deploy owner: %+v", step.Owner)
	}
}

func TestDeliverySpecialOwners(t *testing.T) {
	r := newDeliveryRig(t)
	for _, tc := range []struct{ step, node, kind string }{
		{"accept", "@owner", protocol.OwnerPerson},
		{"test", "@github", protocol.OwnerGitHub},
		{"merge", "@caller", protocol.OwnerCaller},
	} {
		q := protocol.DeliveryOwnerSetRequest{Project: "demo", Step: tc.step, Node: tc.node}
		if err := r.s.SetDeliveryOwner(q, "owner"); err != nil {
			t.Fatalf("set %s: %v", tc.node, err)
		}
		p, err := r.s.deliveryPipeline("demo")
		if err != nil {
			t.Fatal(err)
		}
		step, _ := p.Find(protocol.StepRef{Step: tc.step})
		if step.Owner.Kind != tc.kind {
			t.Fatalf("%s owner = %+v, want %s", tc.step, step.Owner, tc.kind)
		}
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "accept", Node: "@github", Expect: "@owner"}, "owner"); err == nil {
		t.Fatal("GitHub was accepted as accept owner")
	}
	id := r.submit(r.a, r.workerSubmit("feature/person", commitA)).Change.Change.ID
	items, err := r.s.ListInbox()
	if err != nil || len(items) != 1 || !strings.Contains(items[0].Text, id) || !strings.Contains(items[0].Text, "accept") {
		t.Fatalf("person step task = %+v, %v", items, err)
	}
	if _, err := r.s.AnswerInbox(items[0].ID, "approve"); err != nil {
		t.Fatal(err)
	}
	view := r.get(id)
	var githubWaiting, personPassed bool
	for _, step := range view.Steps {
		if step.Step == "test" && strings.Contains(step.Note, "waiting for GitHub") {
			githubWaiting = true
		}
		if step.Step == "accept" && step.State == protocol.StepStatePassed && step.Evidence.By == protocol.EvidenceByOwner {
			personPassed = true
		}
	}
	if !githubWaiting {
		t.Fatalf("GitHub task did not show its waiting state: %+v", view.Steps)
	}
	if !personPassed {
		t.Fatalf("owner task did not record evidence: %+v", view.Steps)
	}
}

func TestDeliverySpecialApproverReadsBackThroughShorthand(t *testing.T) {
	r := newDeliveryRig(t)
	q := protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "approve", Node: "@delegate"}
	if err := r.s.SetDeliveryOwner(q, "owner"); err != nil {
		t.Fatal(err)
	}
	snap, err := r.s.SettingsSnapshot()
	if err != nil || snap.String("delivery.merge_authority", settings.Where{Repo: "demo"}) != "delegated" {
		t.Fatalf("delegate shorthand = %q, %v", snap.String("delivery.merge_authority", settings.Where{Repo: "demo"}), err)
	}
	q.Node, q.Expect = "@none", "@delegate"
	if err := r.s.SetDeliveryOwner(q, "owner"); err != nil {
		t.Fatal(err)
	}
	snap, err = r.s.SettingsSnapshot()
	if err != nil || snap.String("delivery.merge_authority", settings.Where{Repo: "demo"}) != "full" {
		t.Fatalf("none shorthand = %q, %v", snap.String("delivery.merge_authority", settings.Where{Repo: "demo"}), err)
	}
}

func TestDeliveryOwnersDistinguishTestPhases(t *testing.T) {
	r := newDeliveryRig(t)
	q := protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "test", AfterMerge: true, Node: "@github"}
	if err := r.s.SetDeliveryOwner(q, "owner"); err != nil {
		t.Fatal(err)
	}
	p, err := r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := p.Find(protocol.StepRef{Step: protocol.StepTest})
	after, _ := p.Find(protocol.StepRef{Step: protocol.StepTest, AfterMerge: true})
	if before.Owner.Kind != protocol.OwnerCaller || after.Owner.Kind != protocol.OwnerGitHub {
		t.Fatalf("test owners: before %+v, after %+v", before.Owner, after.Owner)
	}
}

func TestDeliveryDeployApprovalCanTightenDelegateToOwner(t *testing.T) {
	r := newDeliveryRig(t)
	change := SettingsChange{Key: "delivery.deploy_rule", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}
	if _, err := r.s.WriteSettings([]SettingsChange{change}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	q := protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "approve", For: "deploy", Node: "@owner"}
	if err := r.s.SetDeliveryOwner(q, "owner"); err != nil {
		t.Fatal(err)
	}
	p, err := r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	step, _ := p.Find(protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepDeploy, AfterMerge: true})
	if step.Owner.Kind != protocol.OwnerPerson {
		t.Fatalf("deploy approver = %+v", step.Owner)
	}
	snap, err := r.s.SettingsSnapshot()
	if err != nil || snap.String("delivery.deploy_rule", settings.Where{Repo: "demo"}) != "owner" {
		t.Fatalf("deploy shorthand = %q, %v", snap.String("delivery.deploy_rule", settings.Where{Repo: "demo"}), err)
	}
}

func TestDeliveryExplicitDeployApprovalCreatesDeployStep(t *testing.T) {
	r := newDeliveryRig(t)
	q := protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "approve", For: "deploy", Node: "@owner"}
	if err := r.s.SetDeliveryOwner(q, "owner"); err != nil {
		t.Fatal(err)
	}
	p, err := r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	approval, ok := p.Find(protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepDeploy, AfterMerge: true})
	if !ok || approval.Owner.Kind != protocol.OwnerPerson {
		t.Fatalf("deploy approval = %+v, found %t", approval, ok)
	}
	if _, ok := p.Find(protocol.StepRef{Step: protocol.StepDeploy, AfterMerge: true}); !ok {
		t.Fatal("deploy step is missing")
	}
}

func TestDeliverySpecialOwnerGraphUndo(t *testing.T) {
	r := newDeliveryRig(t)
	q := protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "test", Node: "@github"}
	if err := r.s.SetDeliveryOwner(q, "owner"); err != nil {
		t.Fatal(err)
	}
	q.Node, q.Expect = "", "@github"
	if err := r.s.SetDeliveryOwner(q, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.GraphUndo(0, "owner"); err != nil {
		t.Fatal(err)
	}
	p, err := r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	step, _ := p.Find(protocol.StepRef{Step: protocol.StepTest})
	if step.Owner.Kind != protocol.OwnerGitHub {
		t.Fatalf("undo owner = %+v", step.Owner)
	}
}

func TestDeliveryOwnerMergeNeedsMergedCommit(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "merge", Node: "@owner"}, "owner"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/owner-merge", commitA)).Change.Change.ID
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepTest}, Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldSuite: protocol.SuiteChanged}})
	items, err := r.s.ListInbox()
	if err != nil || len(items) != 1 || items[0].Delivery == nil || items[0].Delivery.Step != protocol.StepMerge {
		t.Fatalf("owner merge item = %+v, %v", items, err)
	}
	if _, err := r.s.AnswerInbox(items[0].ID, "approve"); err == nil {
		t.Fatal("owner merge passed without a merged commit")
	}
	if _, err := r.s.AnswerInbox(items[0].ID, `approve {"merged_commit":"`+commitMerged+`"}`); err != nil {
		t.Fatal(err)
	}
	change := r.get(id).Change
	if change.State != protocol.ChangeMerged || change.MergedCommit != commitMerged {
		t.Fatalf("owner merge result = %+v", change)
	}
}

func TestDeliveryNodeAssignmentCannotReplaceWrittenApproval(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: "approves"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "network", Scope: "network", Value: json.RawMessage(`"ask"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	p, err := r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	approval, _ := p.Find(protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge})
	if approval.Owner.Kind != protocol.OwnerPerson {
		t.Fatalf("written owner approval was replaced: %+v", approval.Owner)
	}
}

func TestDeliveryTightenedApprovalRejectsQueuedNodeReport(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: "approves"}, "owner"); err != nil {
		t.Fatal(err)
	}
	sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "reviewer", Repo: protocol.RepoInfo{Project: "reviewer"}})
	if err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/approval", commitA)).Change.Change.ID
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "network", Scope: "network", Value: json.RawMessage(`"ask"`)}}, "owner", "tighten"); err != nil {
		t.Fatal(err)
	}
	items, err := r.s.ListInbox()
	if err != nil || len(items) != 1 || !strings.Contains(items[0].Text, id) {
		t.Fatalf("tightened approval was not queued for the owner: %+v, %v", items, err)
	}
	ref := protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}
	if _, err := r.s.ClaimStep(r.b, protocol.StepClaimRequest{SessionID: sid, ChangeID: id, StepRef: ref, Commit: commitA}); err == nil {
		t.Error("a queued node approval could be claimed after the written rule was tightened")
	}
	if _, err := r.report(r.b, protocol.StepReportRequest{SessionID: sid, ChangeID: id, StepRef: ref, Result: protocol.StepResultPass, Commit: commitA}); err == nil {
		t.Fatal("a queued node approval bypassed the tightened written rule")
	}
}

func TestDeliveryUndoTightenedApprovalRestoresNodeTask(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: "approves"}, "owner"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/undo-approval", commitA)).Change.Change.ID
	result, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "network", Scope: "network", Value: json.RawMessage(`"ask"`)}}, "owner", "tighten")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.UndoSettings(result.Revision, "owner"); err != nil {
		t.Fatal(err)
	}
	var owner string
	var completed int
	err = r.s.db.QueryRow(`SELECT owner_node,completed FROM delivery_step_tasks WHERE change_id=? AND step='approve' AND for_step='merge'`, id).Scan(&owner, &completed)
	if err != nil || owner != "reviewer" || completed != 0 {
		t.Fatalf("restored task = %q, completed %d, %v", owner, completed, err)
	}
	items, err := r.s.ListInbox()
	if err != nil || len(items) != 1 || items[0].Status != "dismissed" {
		t.Fatalf("obsolete approval = %+v, %v", items, err)
	}
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "network", Scope: "network", Value: json.RawMessage(`"ask"`)}}, "owner", "tighten again"); err != nil {
		t.Fatal(err)
	}
	items, err = r.s.ListInbox()
	if err != nil || len(items) != 1 || items[0].Status != "open" {
		t.Fatalf("reopened approval = %+v, %v", items, err)
	}
}

func TestDeliveryUndoWrittenApprovalRestoresDelegateTask(t *testing.T) {
	r := newDeliveryRig(t)
	initial := SettingsChange{Key: "delivery.merge_authority", Level: "network", Scope: "network", Value: json.RawMessage(`"delegated"`)}
	if _, err := r.s.WriteSettings([]SettingsChange{initial}, "owner", "delegate"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/undo-delegate", commitA)).Change.Change.ID
	change := SettingsChange{Key: initial.Key, Level: initial.Level, Scope: initial.Scope, Value: json.RawMessage(`"ask"`), Expect: initial.Value}
	result, err := r.s.WriteSettings([]SettingsChange{change}, "owner", "tighten")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.UndoSettings(result.Revision, "owner"); err != nil {
		t.Fatal(err)
	}
	var owner string
	var completed int
	err = r.s.db.QueryRow(`SELECT owner_node,completed FROM delivery_step_tasks WHERE change_id=? AND step='approve' AND for_step='merge'`, id).Scan(&owner, &completed)
	if err != nil || owner != delegateTaskOwner || completed != 0 {
		t.Fatalf("restored delegate task = %q, completed %d, %v", owner, completed, err)
	}
}

func TestDeliverySettingsChangeDispatchesDelegateTasks(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: "governs", Mode: "owner"}, "owner"); err != nil {
		t.Fatal(err)
	}
	sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "reviewer", Repo: protocol.RepoInfo{Project: "reviewer"}})
	if err != nil {
		t.Fatal(err)
	}
	initial := SettingsChange{Key: "delivery.merge_authority", Level: "network", Scope: "network", Value: json.RawMessage(`"ask"`)}
	if _, err := r.s.WriteSettings([]SettingsChange{initial}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/settings-dispatch", commitA)).Change.Change.ID
	change := SettingsChange{Key: initial.Key, Level: initial.Level, Scope: initial.Scope, Value: json.RawMessage(`"delegated"`), Expect: initial.Value}
	if _, err := deliveryCall[map[string]any](r, r.a, "settings.write", protocol.SettingsWrite{Changes: []SettingsChange{change}, By: "owner"}); err != nil {
		t.Fatal(err)
	}
	msgs, err := r.s.Messages(r.b, protocol.QueryMessages{SessionID: sid})
	if err != nil || len(msgs) != 1 || !strings.Contains(msgs[0].Body, id) {
		t.Fatalf("delegate task after settings change = %+v, %v", msgs, err)
	}
}

func TestDeliveryReadyTasksBackfilledAfterUpgrade(t *testing.T) {
	path := seededStorePath(t, "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.tempFolders = []string{}
	r := deliveryRigOn(t, s)
	if err := s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: "reviews"}, "owner"); err != nil {
		t.Fatal(err)
	}
	q := r.workerSubmit("feature/existing", commitA)
	q.Difficulty = "hard"
	id := r.submit(r.a, q).Change.Change.ID
	// A change recorded by build step 1 has no task row yet.
	if _, err := s.db.Exec(`DELETE FROM delivery_step_tasks WHERE change_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var owner string
	if err := s.db.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND step='review'`, id).Scan(&owner); err != nil || owner != "reviewer" {
		t.Fatalf("backfilled task owner = %q, %v", owner, err)
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

func TestDeliveryCallerTaskInSharedPipeline(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: "reviews"}, "owner"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/shared", commitA)).Change.Change.ID
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	msgs, err := r.s.Messages(r.a, protocol.QueryMessages{SessionID: r.caller})
	if err != nil || len(msgs) == 0 || !strings.Contains(msgs[0].Body, id) || !strings.Contains(msgs[0].Body, "steps: accept, test") {
		t.Fatalf("caller task = %+v, %v", msgs, err)
	}
	ref := protocol.StepRef{Step: protocol.StepTest}
	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: r.caller, ChangeID: id, StepRef: ref, Commit: commitA}); err != nil {
		t.Fatal(err)
	}
	running := false
	for _, step := range r.get(id).Steps {
		if step.StepRef == ref && step.State == protocol.StepStateRunning {
			running = true
		}
	}
	if !running {
		t.Fatal("claimed caller step is not shown as running")
	}
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: id, StepRef: ref, Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldSuite: protocol.SuiteChanged}}); err == nil {
		t.Fatal("another session reported the caller's task")
	}
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: ref, Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldSuite: protocol.SuiteChanged}})
}

func TestDeliveryPushPlanFindsRemoteOwner(t *testing.T) {
	r := newDeliveryRig(t)
	remote, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "remote-caller", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	plan := func(machine, caller string, want bool) {
		t.Helper()
		q := map[string]string{"project": "demo"}
		if caller != "" {
			q["caller_session_id"] = caller
		}
		got, err := deliveryCall[protocol.DeliveryNeedsPushResult](r, machine, protocol.FrameDeliveryNeedsPush, q)
		if err != nil || got.NeedsPush != want {
			t.Fatalf("machine %s caller %s: got %+v, %v, want %t", machine, caller, got, err, want)
		}
	}
	plan(r.a, r.caller, false) // Remote unrelated project sessions do not own this worker.
	plan(r.a, remote, true)
	plan(r.a, "", false) // Older daemons only check node owners.
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: "reviews"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "reviewer", Repo: protocol.RepoInfo{Project: "reviewer"}}); err != nil {
		t.Fatal(err)
	}
	plan(r.a, r.caller, true)
	plan(r.b, remote, false)
	plan(r.a, "", true)
}

func TestDeliveryExpiredTaskNoticeCanBeSentAgain(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "reviewer", Repo: protocol.RepoInfo{Project: "reviewer"}})
	if err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/retry", commitA)).Change.Change.ID
	r.g.dispatchStepTasks(context.Background())
	var old string
	if err := r.s.db.QueryRow(`SELECT message_id FROM delivery_step_messages WHERE change_id=? AND session_id=?`, id, sid).Scan(&old); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE messages SET state='expired' WHERE id=?`, old); err != nil {
		t.Fatal(err)
	}
	r.g.dispatchStepTasks(context.Background())
	var next string
	if err := r.s.db.QueryRow(`SELECT message_id FROM delivery_step_messages WHERE change_id=? AND session_id=?`, id, sid).Scan(&next); err != nil || next == old {
		t.Fatalf("task notice did not retry: old %q, new %q, error %v", old, next, err)
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
	for _, actor := range []string{"CTO (minor)", "CTO (owner window)"} {
		if err := r.s.SetEdge(e, actor); err == nil {
			t.Errorf("%s could assign a merge owner", actor)
		}
	}
	raw, _ := json.Marshal(e)
	if class, err := r.s.CTOGraphClass("edge_set", raw); err != nil || class != "rule" {
		t.Fatalf("CTO class = %q, %v", class, err)
	}
	if err := r.s.SetEdge(e, "cto"); err != nil {
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

func TestDeliveryNodeTaskExplicitClaim(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("merger", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	var sessions []string
	for _, name := range []string{"one", "two"} {
		id, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: name, Repo: protocol.RepoInfo{Project: "merger"}})
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, id)
	}
	id := r.submit(r.a, r.workerSubmit("feature/claim", commitA)).Change.Change.ID
	claim := func(machine, session, commit string) error {
		_, err := deliveryCall[map[string]any](r, machine, "step.claim", map[string]any{"session_id": session, "change_id": id, "step": "accept", "commit": commit})
		return err
	}
	if err := claim(r.b, sessions[0], commitB); err == nil {
		t.Fatal("claim accepted an old commit")
	}
	if err := claim(r.a, sessions[0], commitA); err == nil {
		t.Fatal("another machine could claim for the owner session")
	}
	if err := claim(r.a, r.caller, commitA); err == nil {
		t.Fatal("the caller could claim another node's step")
	}
	if err := claim(r.b, sessions[0], commitA); err != nil {
		t.Fatalf("owner claim: %v", err)
	}
	if err := claim(r.b, sessions[0], commitA); err != nil {
		t.Fatalf("repeat claim: %v", err)
	}
	pending, err := r.s.Pending(r.b, false)
	if err != nil || len(pending) != 1 || pending[0].To != sessions[0] {
		t.Fatalf("unclaimed sessions still have pending tasks: %+v, %v", pending, err)
	}
	v := r.get(id)
	for _, step := range v.Steps {
		if step.Step == protocol.StepAccept && (step.State != "running" || step.Note != "claimed by "+sessions[0]) {
			t.Fatalf("claimed step state: %+v", step)
		}
	}
	for _, step := range v.Next {
		if step == stepAccept {
			t.Fatal("claimed step still appears unclaimed")
		}
	}
	if err := claim(r.b, sessions[1], commitA); err == nil {
		t.Fatal("second session claimed the same task")
	}
	report := protocol.StepReportRequest{SessionID: sessions[1], ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA}
	if _, err := r.report(r.b, report); err == nil {
		t.Fatal("second session could report the claimed task")
	}
	report.SessionID = sessions[0]
	if _, err := r.report(r.b, report); err != nil {
		t.Fatalf("claimant report: %v", err)
	}
}

func TestDeliveryClaimKeepsOwnerAfterAssignmentChanges(t *testing.T) {
	r := newDeliveryRig(t)
	for _, name := range []string{"merger", "other"} {
		if err := r.s.AddArchitect(name, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	edge := protocol.Edge{From: "merger", To: "demo", Kind: "accepts"}
	if err := r.s.SetEdge(edge, "owner"); err != nil {
		t.Fatal(err)
	}
	sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "merger", Repo: protocol.RepoInfo{Project: "merger"}})
	if err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/owner-change", commitA)).Change.Change.ID
	_, err = deliveryCall[protocol.StepClaimResult](r, r.b, "step.claim", map[string]any{"session_id": sid, "change_id": id, "step": "accept", "commit": commitA})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.s.RemoveEdge(edge, "owner"); err != nil {
		t.Fatal(err)
	}
	edge.From = "other"
	if err := r.s.SetEdge(edge, "owner"); err != nil {
		t.Fatal(err)
	}
	v := r.get(id)
	for _, step := range v.Steps {
		if step.Step == protocol.StepAccept && (step.Owner.Node != "merger" || step.State != "running") {
			t.Fatalf("claimed task changed owner: %+v", step)
		}
	}
	if _, err := r.report(r.b, protocol.StepReportRequest{SessionID: sid, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA}); err != nil {
		t.Fatalf("original owner could not report: %v", err)
	}
}

func TestDeliveryNodeTaskConcurrentClaims(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("merger", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	var sessions []string
	for _, native := range []string{"first", "second"} {
		id, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: native, Repo: protocol.RepoInfo{Project: "merger"}})
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, id)
	}
	id := r.submit(r.a, r.workerSubmit("feature/concurrent", commitA)).Change.Change.ID
	start := make(chan struct{})
	results := make(chan error, len(sessions))
	for _, sid := range sessions {
		go func() {
			<-start
			_, err := r.s.ClaimStep(r.b, protocol.StepClaimRequest{SessionID: sid, ChangeID: id, StepRef: stepAccept, Commit: commitA})
			results <- err
		}()
	}
	close(start)
	var successes int
	for range sessions {
		if err := <-results; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("%d claims succeeded; want exactly one", successes)
	}
}

func TestDeliveryTaskIncludesRevisionDiffStat(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("merger", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "merger", Repo: protocol.RepoInfo{Project: "merger"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r.workerSubmit("feature/stat", commitA))
	var q map[string]any
	if err := json.Unmarshal(raw, &q); err != nil {
		t.Fatal(err)
	}
	q["diff_stat"] = "README.md | 1 +\n1 file changed, 1 insertion(+)"
	q["uncommitted"] = true
	if _, err := deliveryCall[protocol.ChangeSubmitResult](r, r.a, protocol.FrameChangeSubmit, q); err != nil {
		t.Fatal(err)
	}
	msgs, err := r.s.Messages(r.b, protocol.QueryMessages{SessionID: sid})
	if err != nil || len(msgs) != 1 || !strings.Contains(msgs[0].Body, "README.md | 1 +") || !strings.Contains(msgs[0].Body, "uncommitted: true") {
		t.Fatalf("task lacks diff stat: %+v, %v", msgs, err)
	}
}

func TestDeliveryDoesNotSendATaskForAnOldRevision(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("merger", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/stale", commitA)).Change.Change.ID
	q := r.workerSubmit("feature/stale", commitB)
	r.submit(r.a, q)
	sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "merger", Repo: protocol.RepoInfo{Project: "merger"}})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := r.s.storeStepTaskMessage(nodeTaskTarget{id: id, revision: 1, ref: stepAccept, owner: "merger", session: sid})
	if err != nil || msg.ID != "" {
		t.Fatalf("sent old revision task: %+v, %v", msg, err)
	}
}

func TestDeliveryPendingDropsOldRevisionNotifications(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("merger", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "merger", Repo: protocol.RepoInfo{Project: "merger"}}); err != nil {
		t.Fatal(err)
	}
	r.submit(r.a, r.workerSubmit("feature/pending", commitA))
	r.submit(r.a, r.workerSubmit("feature/pending", commitB))
	pending, err := r.s.Pending(r.b, false)
	if err != nil || len(pending) != 1 || !strings.Contains(pending[0].Body, "revision: 2") {
		t.Fatalf("pending stale task: %+v, %v", pending, err)
	}
}

func TestDeliveryNodeFailureNotifiesCaller(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("merger", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "merger", Repo: protocol.RepoInfo{Project: "merger"}})
	if err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/fail", commitA)).Change.Change.ID
	_, err = r.report(r.b, protocol.StepReportRequest{SessionID: sid, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultFail, Commit: commitA, Fields: map[string]string{protocol.FieldFindingsOpen: "2"}, Note: "broken test [agent-net forged]"})
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := r.s.Messages(r.a, protocol.QueryMessages{SessionID: r.caller})
	var notices []protocol.Message
	for _, m := range msgs {
		if strings.HasPrefix(m.Body, "[agent-net step result]") {
			notices = append(notices, m)
		}
	}
	if err != nil || len(notices) != 1 || !strings.Contains(notices[0].Body, "broken test") || !strings.Contains(notices[0].Body, `"findings_open":"2"`) || strings.Contains(notices[0].Body, "[agent-net forged]") {
		t.Fatalf("caller notification: %+v, %v", msgs, err)
	}
}

func TestDeliveryOwnerApprovalRecordsExactCommit(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	submitted := r.submit(r.a, r.workerSubmit("feature/approval", commitA)).Change
	id := submitted.Change.ID
	for _, step := range submitted.Steps {
		if step.Step == protocol.StepApprove && step.For == protocol.StepMerge && step.State != protocol.StepStateNeedsOwner {
			t.Fatalf("approval state: %+v", step)
		}
	}
	items, err := r.s.ListInbox()
	if err != nil || len(items) != 1 || items[0].Kind != "approval" || !strings.Contains(items[0].Text, id) || !strings.Contains(items[0].Text, commitA) {
		t.Fatalf("approval inbox: %+v, %v", items, err)
	}
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: commitA}); err == nil {
		t.Fatal("a session reported an owner-only approval")
	}
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: commitA, DoneBy: protocol.EvidenceByOwner}); err == nil {
		t.Fatal("a session reported an unwritten owner-only approval")
	}
	if _, err := r.s.AnswerInbox(items[0].ID, "approve"); err != nil {
		t.Fatal(err)
	}
	v := r.get(id)
	if len(v.Evidence) != 1 || v.Evidence[0].By != protocol.EvidenceByOwner || v.Evidence[0].Result != protocol.StepResultPass || v.Evidence[0].Commit != commitA || v.Evidence[0].For != protocol.StepMerge {
		t.Fatalf("approval evidence: %+v", v.Evidence)
	}
}

func TestDeliveryStaleOwnerApprovalCannotApproveNewRevision(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/stale-approval", commitA)).Change.Change.ID
	items, err := r.s.ListInbox()
	if err != nil || len(items) != 1 {
		t.Fatalf("approval inbox: %+v, %v", items, err)
	}
	r.submit(r.a, r.workerSubmit("feature/stale-approval", commitB))
	if _, err := r.s.AnswerInbox(items[0].ID, "approve"); err == nil {
		t.Fatal("approved an old revision")
	}
	v := r.get(id)
	if len(v.Evidence) != 0 {
		t.Fatalf("stale approval recorded: %+v", v.Evidence)
	}
	oldID := items[0].ID
	items, err = r.s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == oldID && item.Status != "dismissed" {
			t.Fatalf("stale approval was not closed: %+v", item)
		}
	}
}

func TestDeliveryOwnerRefusalNotifiesCaller(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/refused", commitA)).Change.Change.ID
	items, err := r.s.ListInbox()
	if err != nil || len(items) != 1 {
		t.Fatalf("approval inbox: %+v, %v", items, err)
	}
	if _, err := r.s.AnswerInbox(items[0].ID, "some text"); err == nil {
		t.Fatal("free text counted as a delivery decision")
	}
	if _, err := r.s.AnswerInbox(items[0].ID, "refuse"); err != nil {
		t.Fatal(err)
	}
	v := r.get(id)
	if len(v.Evidence) != 1 || v.Evidence[0].By != protocol.EvidenceByOwner || v.Evidence[0].Result != protocol.StepResultFail {
		t.Fatalf("refusal evidence: %+v", v.Evidence)
	}
	msgs, err := r.s.Messages(r.a, protocol.QueryMessages{SessionID: r.caller})
	var notices []protocol.Message
	for _, m := range msgs {
		if strings.HasPrefix(m.Body, "[agent-net step result]") {
			notices = append(notices, m)
		}
	}
	if err != nil || len(notices) != 1 || !strings.Contains(notices[0].Body, "result: fail") {
		t.Fatalf("caller notice: %+v, %v", msgs, err)
	}
}

func TestDeliveryDelegatedApprovalRoutesAndCanBeClaimed(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"delegated"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.AddArchitect("architect", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "architect", To: "demo", Kind: "governs", Mode: "arbiter"}, "owner"); err != nil {
		t.Fatal(err)
	}
	sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "architect", Repo: protocol.RepoInfo{Project: "architect"}})
	if err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/delegate", commitA)).Change.Change.ID
	items, err := r.s.ListInbox()
	if err != nil || len(items) != 1 || !strings.Contains(items[0].Text, commitA) {
		t.Fatalf("owner fallback approval: %+v, %v", items, err)
	}
	msgs, err := r.s.Messages(r.b, protocol.QueryMessages{SessionID: sid})
	if err != nil || len(msgs) != 1 || !strings.Contains(msgs[0].Body, "step: approve for merge") {
		t.Fatalf("delegate task: %+v, %v", msgs, err)
	}
	claim := protocol.StepClaimRequest{SessionID: sid, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}, Commit: commitA}
	if _, err := deliveryCall[protocol.StepClaimResult](r, r.b, "step.claim", claim); err != nil {
		t.Fatal(err)
	}
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: claim.StepRef, Result: protocol.StepResultPass, Commit: commitA}); err == nil {
		t.Fatal("non-delegate reported the approval")
	}
	result, err := r.report(r.b, protocol.StepReportRequest{SessionID: sid, ChangeID: id, StepRef: claim.StepRef, Result: protocol.StepResultPass, Commit: commitA})
	if err != nil || result.Evidence.By != sid {
		t.Fatalf("delegate report: %+v, %v", result, err)
	}
	if _, err := r.s.AnswerInbox(items[0].ID, "approve"); err == nil {
		t.Fatal("owner approved a task already reported by a delegate")
	}
}

func TestDeliveryDelegateOwnerCanApproveFirst(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"delegated"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.AddArchitect("architect", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "architect", To: "demo", Kind: "governs", Mode: "owner"}, "owner"); err != nil {
		t.Fatal(err)
	}
	sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "architect", Repo: protocol.RepoInfo{Project: "architect"}})
	if err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/delegate-owner", commitA)).Change.Change.ID
	claim := protocol.StepClaimRequest{SessionID: sid, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}, Commit: commitA}
	if _, err := deliveryCall[protocol.StepClaimResult](r, r.b, "step.claim", claim); err != nil {
		t.Fatal(err)
	}
	items, err := r.s.ListInbox()
	if err != nil || len(items) != 1 {
		t.Fatalf("owner fallback approval: %+v, %v", items, err)
	}
	if _, err := r.s.AnswerInbox(items[0].ID, "approve"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.report(r.b, protocol.StepReportRequest{SessionID: sid, ChangeID: id, StepRef: claim.StepRef, Result: protocol.StepResultPass, Commit: commitA}); err == nil {
		t.Fatal("delegate reported after the owner approved")
	}
	v := r.get(id)
	if len(v.Evidence) != 1 || v.Evidence[0].By != protocol.EvidenceByOwner || v.Evidence[0].Commit != commitA {
		t.Fatalf("owner evidence: %+v", v.Evidence)
	}
}

func TestDeliveryDelegateDirectOwnerApproval(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"delegated"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/direct-owner", commitA)).Change.Change.ID
	q := protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: commitA, DoneBy: protocol.EvidenceByOwner}
	result, err := r.report(r.a, q)
	if err != nil || result.Evidence.By != protocol.EvidenceByOwner || result.Evidence.ReportedBy != r.caller || result.Evidence.Fields[protocol.FieldIndependent] != protocol.IndependentFamily {
		t.Fatalf("direct owner approval: %+v, %v", result, err)
	}
	items, err := r.s.ListInbox()
	if err != nil || len(items) != 1 || items[0].Status != "dismissed" {
		t.Fatalf("owner inbox after direct approval: %+v, %v", items, err)
	}
}

func TestDeliveryDelegateModesControlWhoCanClaim(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprintf("advisor_allowed_%t", allowed), func(t *testing.T) {
			r := newDeliveryRig(t)
			changes := []SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"delegated"`)}}
			if allowed {
				changes = append(changes, SettingsChange{Key: "delivery.delegated_approver_modes", Level: "network", Scope: "network", Value: json.RawMessage(`["advisor","arbiter","owner"]`)})
			}
			if _, err := r.s.WriteSettings(changes, "owner", "test"); err != nil {
				t.Fatal(err)
			}
			if err := r.s.AddArchitect("advisor", "owner"); err != nil {
				t.Fatal(err)
			}
			if err := r.s.SetEdge(protocol.Edge{From: "advisor", To: "demo", Kind: "governs", Mode: "advisor"}, "owner"); err != nil {
				t.Fatal(err)
			}
			sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "advisor", Repo: protocol.RepoInfo{Project: "advisor"}})
			if err != nil {
				t.Fatal(err)
			}
			id := r.submit(r.a, r.workerSubmit("feature/modes", commitA)).Change.Change.ID
			claim := protocol.StepClaimRequest{SessionID: sid, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}, Commit: commitA}
			_, err = deliveryCall[protocol.StepClaimResult](r, r.b, "step.claim", claim)
			if (err == nil) != allowed {
				t.Fatalf("advisor claim = %v, want allowed=%t", err, allowed)
			}
		})
	}
}

func TestInboxApprovalClosesAfterStep(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"delegated"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/inbox-step", commitA)).Change.Change.ID
	items, err := r.s.ListInbox()
	if err != nil || len(items) != 1 || items[0].Status != "open" {
		t.Fatalf("before: %+v %v", items, err)
	}
	q := protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: commitA, DoneBy: protocol.EvidenceByOwner}
	if _, err := r.report(r.a, q); err != nil {
		t.Fatal(err)
	}
	items, err = r.s.ListInbox()
	if err != nil || len(items) != 1 || items[0].Status == "open" {
		t.Fatalf("after: %+v %v", items, err)
	}
}
