package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMergerAfterMergeTestRouting(t *testing.T) {
	r := newDeliveryRig(t)
	c := r.submit(r.a, r.workerSubmit("feature/after", commitA)).Change.Change
	if _, err := r.s.db.Exec(`UPDATE delivery_changes SET state='merged',merged_commit=? WHERE id=?`, commitMerged, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`INSERT INTO delivery_step_tasks(change_id,revision,step,for_step,after_merge,owner_node,created_at) VALUES(?,1,'test','',1,'/caller',0)`, c.ID); err != nil {
		t.Fatal(err)
	}
	q := protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet, Configure: true}
	if err := r.s.SetMerger(q, "owner"); err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		var got string
		if err := r.s.db.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND step='test' AND after_merge=1`, c.ID).Scan(&got); err != nil || got != want {
			t.Fatalf("after-merge owner=%q want=%q err=%v", got, want, err)
		}
	}
	check("/merger")
	p, err := r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	step, _ := p.Find(protocol.StepRef{Step: "test", AfterMerge: true})
	if step.Owner.Kind != protocol.OwnerMerger {
		t.Fatalf("new task pipeline owner=%+v", step.Owner)
	}
	if _, err := r.s.db.Exec(`DELETE FROM delivery_step_tasks WHERE change_id=? AND after_merge=1`, c.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := r.s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	c.State, c.MergedCommit = protocol.ChangeMerged, commitMerged
	if err = r.s.queueSteps(tx, c, p, 0); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	check("/merger")
	q.SessionID = ""
	if err := r.s.SetMerger(q, "owner"); err != nil {
		t.Fatal(err)
	}
	check("/caller")
}

func TestDeliveryStepTaskBatch(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "review", Node: "reviewer"}, "owner"); err != nil {
		t.Fatal(err)
	}
	// Accumulate a backlog without dispatching to the caller on each submit.
	if _, err := r.s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, r.submit(r.a, r.workerSubmit(fmt.Sprintf("feature/batch%d", i), commitA)).Change.Change.ID)
	}
	if _, err := r.s.db.Exec(`DELETE FROM delivery_step_tasks WHERE step<>'accept'`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET online=1 WHERE id=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	r.g.dispatchStepTasks(context.Background())
	check := func(want int) {
		t.Helper()
		var n int
		if err := r.s.db.QueryRow(`SELECT count(DISTINCT message_id) FROM delivery_step_messages WHERE session_id=?`, r.caller).Scan(&n); err != nil || n != want {
			t.Fatalf("messages=%d want=%d err=%v", n, want, err)
		}
	}
	check(1)
	var body string
	if err := r.s.db.QueryRow(`SELECT body FROM messages WHERE id IN (SELECT message_id FROM delivery_step_messages WHERE session_id=?)`, r.caller).Scan(&body); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if !strings.Contains(body, id) {
			t.Fatalf("batch missing %s: %s", id, body)
		}
	}
	r.g.dispatchStepTasks(context.Background())
	check(1)
	r.submit(r.a, r.workerSubmit("feature/later", commitA))
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	check(2)
	if _, err := r.s.db.Exec(`UPDATE messages SET state='expired' WHERE id IN (SELECT message_id FROM delivery_step_messages WHERE session_id=?)`, r.caller); err != nil {
		t.Fatal(err)
	}
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	check(1)
	var listed int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_step_messages WHERE session_id=?`, r.caller).Scan(&listed); err != nil || listed != 5 {
		t.Fatalf("listed=%d want=5 err=%v", listed, err)
	}
}

func TestMergerMergeReportQueuesAfterMergeTest(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit-caller=%t", explicit), func(t *testing.T) {
			r := newDeliveryRig(t)
			if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet, Configure: true}, "owner"); err != nil {
				t.Fatal(err)
			}
			owner, session := "/merger", r.sonnet
			if explicit {
				if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "test", AfterMerge: true, Node: "@caller"}, "owner"); err != nil {
					t.Fatal(err)
				}
				owner, session = "/caller", r.caller
			}
			c := r.submit(r.a, r.workerSubmit("feature/merge-report", commitA)).Change.Change
			r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept, Result: "pass", Commit: commitA})
			r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: c.ID, StepRef: protocol.StepRef{Step: "test"}, Result: "pass", Commit: commitA, Fields: map[string]string{"suite": "changed"}})
			yes := true
			r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: c.ID, StepRef: protocol.StepRef{Step: "merge"}, Result: "pass", Commit: commitA, Fields: map[string]string{"merged_commit": commitMerged}, MergeContainsHead: &yes})
			var got string
			if err := r.s.db.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND step='test' AND after_merge=1`, c.ID).Scan(&got); err != nil || got != owner {
				t.Fatalf("new after-merge owner=%q want=%q err=%v", got, owner, err)
			}
			ref := protocol.StepRef{Step: "test", AfterMerge: true}
			if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: session, ChangeID: c.ID, StepRef: ref, Commit: commitMerged}); err != nil {
				t.Fatal(err)
			}
			r.mustReport(r.a, protocol.StepReportRequest{SessionID: session, ChangeID: c.ID, StepRef: ref, Result: "pass", Commit: commitMerged, Fields: map[string]string{"suite": "full"}})
		})
	}
}

func TestMergerReleaseRouting(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet, Configure: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "release", AfterMerge: true, Node: "@merger"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "test", AfterMerge: true, Node: "@caller"}, "owner"); err != nil {
		t.Fatal(err)
	}
	c := r.submit(r.a, r.workerSubmit("feature/release", commitA)).Change.Change
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept, Result: "pass", Commit: commitA})
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: c.ID, StepRef: protocol.StepRef{Step: "test"}, Result: "pass", Commit: commitA, Fields: map[string]string{"suite": "changed"}})
	yes := true
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: c.ID, StepRef: protocol.StepRef{Step: "merge"}, Result: "pass", Commit: commitA, Fields: map[string]string{"merged_commit": commitMerged}, MergeContainsHead: &yes})
	ref := protocol.StepRef{Step: "release", AfterMerge: true}
	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: r.sonnet, ChangeID: c.ID, StepRef: ref, Commit: commitMerged}); err == nil {
		t.Fatal("release claimed before full CI")
	}
	if err := r.s.SetSessionOffline(r.a, r.sonnet); err != nil {
		t.Fatal(err)
	}
	r.g.logger = log.New(io.Discard, "", 0)
	r.g.conns = map[string]*nodeConn{}
	wakes := make(chan string, 1)
	fakeOwnerNode(t, r.g, r.a, func(h protocol.Header, raw []byte) error {
		if h.Type == protocol.FrameSessionWake {
			var p struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			wakes <- p.SessionID
		}
		return nil
	})
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: protocol.StepRef{Step: "test", AfterMerge: true}, Result: "pass", Commit: commitMerged, Fields: map[string]string{"suite": "full"}})
	select {
	case id := <-wakes:
		if id != r.sonnet {
			t.Fatalf("woke %s, want %s", id, r.sonnet)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retired merger was not woken for release")
	}
	if _, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "sonnet", Repo: protocol.RepoInfo{Project: "demo"}}); err != nil {
		t.Fatal(err)
	}
	r.g.dispatchStepTasks(context.Background())
	var n int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_step_messages WHERE change_id=? AND step='release' AND session_id=?`, c.ID, r.sonnet).Scan(&n); err != nil || n != 1 {
		t.Fatalf("release notices=%d err=%v", n, err)
	}
	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: r.sonnet, ChangeID: c.ID, StepRef: ref, Commit: commitMerged}); err != nil {
		t.Fatal(err)
	}
}

func TestDeliveryTestWaitsForAcceptAndKeepsExistingTask(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet, Configure: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	c := r.submit(r.a, r.workerSubmit("feature/order", commitA)).Change.Change
	count := func(rev int) int {
		t.Helper()
		var n int
		if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_step_tasks WHERE change_id=? AND revision=? AND step='test' AND after_merge=0`, c.ID, rev).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(1) != 0 {
		t.Fatal("test queued before accept")
	}
	q := protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept, Result: protocol.StepResultFail, Commit: commitA}
	r.mustReport(r.a, q)
	if count(1) != 0 {
		t.Fatal("test queued after failed accept")
	}
	c = r.submit(r.a, r.workerSubmit("feature/pass", commitA)).Change.Change
	q.ChangeID = c.ID
	q.Result = protocol.StepResultPass
	r.mustReport(r.a, q)
	if count(1) != 1 {
		t.Fatal("test not queued after accept")
	}
	r.submit(r.a, r.workerSubmit("feature/pass", commitB))
	if count(2) != 0 {
		t.Fatal("test queued before accept on new revision")
	}
	// A task already claimed under the old rule survives gateway backfill.
	if _, err := r.s.db.Exec(`INSERT INTO delivery_step_tasks(change_id,revision,step,for_step,after_merge,owner_node,claimed_by,created_at) VALUES(?,2,'test','',0,'/merger',?,0)`, c.ID, r.sonnet); err != nil {
		t.Fatal(err)
	}
	if err := r.s.backfillDeliveryTasks(); err != nil {
		t.Fatal(err)
	}
	for _, step := range r.get(c.ID).Steps {
		if step.StepRef == (protocol.StepRef{Step: "test"}) && step.State != protocol.StepStateRunning {
			t.Fatalf("claimed old task state = %s", step.State)
		}
	}
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitB})
	var claimed string
	if err := r.s.db.QueryRow(`SELECT claimed_by FROM delivery_step_tasks WHERE change_id=? AND revision=2 AND step='test' AND after_merge=0`, c.ID).Scan(&claimed); err != nil || claimed != r.sonnet || count(2) != 1 {
		t.Fatalf("claimed=%q err=%v", claimed, err)
	}
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: c.ID, StepRef: protocol.StepRef{Step: "test"}, Result: protocol.StepResultPass, Commit: commitB, Fields: map[string]string{"suite": "changed"}})
}
