package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMergerRoutingAndRegistration(t *testing.T) {
	r := newDeliveryRig(t)
	q := protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet}
	if err := r.s.SetMerger(q, "cto"); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"test", "merge"} {
		if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: step, Node: "@merger"}, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	a, _, err := r.s.Agent(r.sonnet)
	if err != nil || a.Type != "merger" {
		t.Fatalf("type=%q err=%v", a.Type, err)
	}

	page, err := r.s.FindSessions(protocol.SessionsFindRequest{Kind: []string{"merger"}})
	if err != nil || len(page.Items) != 1 || page.Items[0].Type != "merger" {
		t.Fatalf("find=%+v err=%v", page, err)
	}
	detail, err := r.s.GetSession(protocol.SessionGetRequest{Session: r.sonnet})
	if err != nil || detail.Session.Type != "merger" {
		t.Fatalf("get=%+v err=%v", detail, err)
	}
	synced, err := r.s.SyncSessions(r.a, []protocol.SessionInfo{{Harness: "claude", NativeID: "sonnet", Repo: protocol.RepoInfo{Project: "demo"}}})
	if err != nil || len(synced) != 1 || synced[0].Type != "merger" {
		t.Fatalf("sync=%+v err=%v", synced, err)
	}
	// Keep the caller and worker online after exercising the complete sync.
	for _, native := range []string{"caller", "worker"} {
		if _, err := r.s.db.Exec(`UPDATE sessions SET online=1 WHERE node_id=? AND native_id=?`, r.a, native); err != nil {
			t.Fatal(err)
		}
	}
	c := r.submit(r.a, r.workerSubmit("feature", commitA)).Change.Change
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	r.g.dispatchStepTasks(context.Background())
	rows, err := r.s.db.Query(`SELECT session_id FROM delivery_step_messages WHERE change_id=? AND step='test'`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	rows.Close()
	if len(got) != 1 || got[0] != r.sonnet {
		t.Fatalf("targets=%v", got)
	}
	claim := protocol.StepClaimRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: protocol.StepRef{Step: "test"}, Commit: commitA}
	if _, err := deliveryCall[protocol.StepClaimResult](r, r.a, protocol.FrameStepClaim, claim); err == nil {
		t.Fatal("other session claimed merger task")
	}
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: claim.StepRef, Commit: commitA, Result: "pass", Fields: map[string]string{"suite": "changed"}}); err == nil {
		t.Fatal("other session reported merger task")
	}
	if err := r.s.SetSessionOffline(r.a, r.sonnet); err != nil {
		t.Fatal(err)
	}
	view, err := r.s.GetChange(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, st := range view.Steps {
		if st.Step == "test" && !st.AfterMerge {
			found = strings.Contains(st.Note, "waiting for merger")
		}
	}
	if !found {
		t.Fatalf("offline status=%+v", view.Steps)
	}
	claim.SessionID = r.sonnet
	if _, err := deliveryCall[protocol.StepClaimResult](r, r.a, protocol.FrameStepClaim, claim); err == nil {
		t.Fatal("offline merger claimed")
	}
	if _, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "sonnet", Repo: protocol.RepoInfo{Project: "demo"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := deliveryCall[protocol.StepClaimResult](r, r.a, protocol.FrameStepClaim, claim); err != nil {
		t.Fatal(err)
	}

	if err := r.s.SetSessionOffline(r.a, r.sonnet); err != nil {
		t.Fatal(err)
	}
	view, err = r.s.GetChange(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range view.Steps {
		if st.StepRef == claim.StepRef && !strings.Contains(st.Note, "waiting for merger") {
			t.Fatalf("claimed offline merger state=%s note=%s", st.State, st.Note)
		}
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET online=1 WHERE id=?`, r.sonnet); err != nil {
		t.Fatal(err)
	}
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: c.ID, StepRef: claim.StepRef, Commit: commitA, Result: "pass", Fields: map[string]string{"suite": "changed"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`DELETE FROM sessions WHERE id=?`, r.sonnet); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM project_mergers`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("records=%d err=%v", n, err)
	}
}

func TestMergerLifecycleReassignsEveryPhase(t *testing.T) {
	for _, action := range []string{"stop", "delete", "replace"} {
		for _, after := range []bool{false, true} {
			t.Run(action+map[bool]string{false: "/before", true: "/after"}[after], func(t *testing.T) {
				r := newDeliveryRig(t)
				q := protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet, Configure: true}
				if err := r.s.SetMerger(q, "owner"); err != nil {
					t.Fatal(err)
				}
				if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "test", AfterMerge: true, Node: "@merger"}, "owner"); err != nil {
					t.Fatal(err)
				}
				c := r.submit(r.a, r.workerSubmit("feature/lifecycle", commitA)).Change.Change
				r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept, Result: "pass", Commit: commitA})
				ref := protocol.StepRef{Step: "test", AfterMerge: after}
				commit := commitA
				if after {
					r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: c.ID, StepRef: protocol.StepRef{Step: "test"}, Result: "pass", Commit: commitA, Fields: map[string]string{"suite": "changed"}})
					yes := true
					r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: c.ID, StepRef: protocol.StepRef{Step: "merge"}, Result: "pass", Commit: commitA, Fields: map[string]string{"merged_commit": commitMerged}, MergeContainsHead: &yes})
					commit = commitMerged
				}
				claim := protocol.StepClaimRequest{SessionID: r.sonnet, ChangeID: c.ID, StepRef: ref, Commit: commit}
				if _, err := r.s.ClaimStep(r.a, claim); err != nil {
					t.Fatal(err)
				}
				// Stop must not rewrite an independently chosen accept owner.
				if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "accept", Node: "@owner", Expect: "@caller"}, "owner"); err != nil {
					t.Fatal(err)
				}
				next := r.caller
				switch action {
				case "stop":
					q.SessionID = ""
					if err := r.s.SetMerger(q, "owner"); err != nil {
						t.Fatal(err)
					}
				case "delete":
					if _, err := r.s.db.Exec(`DELETE FROM sessions WHERE id=?`, r.sonnet); err != nil {
						t.Fatal(err)
					}
				case "replace":
					var err error
					next, err = r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "replacement", Repo: protocol.RepoInfo{Project: "demo"}})
					if err != nil {
						t.Fatal(err)
					}
					q.SessionID, q.Configure = next, false
					if err := r.s.SetMerger(q, "owner"); err != nil {
						t.Fatal(err)
					}
				}
				p, err := r.s.deliveryPipeline("demo")
				if err != nil {
					t.Fatal(err)
				}
				accept, _ := p.Find(stepAccept)
				if accept.Owner.Kind != protocol.OwnerPerson {
					t.Fatal("accept owner changed")
				}
				if _, err := r.s.ClaimStep(r.a, claim); err == nil {
					t.Fatal("old merger retained claim")
				}
				r.g.dispatchStepTasks(context.Background())
				var messages int
				if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_step_messages WHERE change_id=? AND step=? AND after_merge=? AND session_id=?`, c.ID, ref.Step, boolInt(after), next).Scan(&messages); err != nil || messages != 1 {
					t.Fatalf("new owner notices=%d err=%v", messages, err)
				}
				claim.SessionID = next
				if _, err := r.s.ClaimStep(r.a, claim); err != nil {
					t.Fatalf("new owner cannot claim: %v", err)
				}
				r.mustReport(r.a, protocol.StepReportRequest{SessionID: next, ChangeID: c.ID, StepRef: ref, Result: "pass", Commit: commit, Fields: map[string]string{"suite": "changed"}})
			})
		}
	}
}

func TestMergerRegistrationMachineAuthority(t *testing.T) {
	r := newDeliveryRig(t)
	q := map[string]any{"project": "demo", "node_id": r.a, "session_id": r.sonnet, "by": "owner"}
	if _, err := deliveryCall[map[string]any](r, r.b, "merger.set", q); err == nil {
		t.Fatal("peer node registered another machine's session")
	}
	q["node_id"] = r.b
	if _, err := deliveryCall[map[string]any](r, r.b, "merger.set", q); err == nil {
		t.Fatal("peer disguised foreign session as local")
	}
	q["node_id"] = r.a
	owner, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	q["owner_session"] = r.caller
	if _, err := deliveryCall[map[string]any](r, r.b, "merger.set", q); err == nil {
		t.Fatal("agent supplied owner authority")
	}
	foreign, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	q["owner_session"] = foreign
	if _, err := deliveryCall[map[string]any](r, r.b, "merger.set", q); err == nil {
		t.Fatal("another machine's owner authorized peer")
	}
	q["owner_session"] = owner
	q["by"] = "owner (CTO proposal p_1)"
	if _, err := deliveryCall[map[string]any](r, r.b, "merger.set", q); err != nil {
		t.Fatalf("approved owner relay refused: %v", err)
	}
	q["session_id"] = ""
	delete(q, "owner_session")
	if _, err := deliveryCall[map[string]any](r, r.b, "merger.set", q); err == nil {
		t.Fatal("peer cleared another machine's merger")
	}
	q["owner_session"] = owner
	if _, err := deliveryCall[map[string]any](r, r.b, "merger.set", q); err != nil {
		t.Fatalf("owner relay clear refused: %v", err)
	}

}

func TestMergerStartMovesExistingCallerClaim(t *testing.T) {
	r := newDeliveryRig(t)
	// A shared pipeline creates a caller test task even before merger setup.
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "review", Node: "reviewer"}, "owner"); err != nil {
		t.Fatal(err)
	}
	c := r.submit(r.a, r.workerSubmit("feature/existing", commitA)).Change.Change
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	claim := protocol.StepClaimRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: protocol.StepRef{Step: "test"}, Commit: commitA}
	if _, err := r.s.ClaimStep(r.a, claim); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet, Configure: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	claim.SessionID = r.sonnet
	if _, err := r.s.ClaimStep(r.a, claim); err != nil {
		t.Fatal(err)
	}
}

// A configured stop without a registration must still return the project's
// @merger assignments to the caller.
func TestMergerConfiguredStopWithoutRegistration(t *testing.T) {
	r := newDeliveryRig(t)
	for _, step := range []string{"test", "merge"} {
		if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: step, Node: "@merger"}, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	c := r.submit(r.a, r.workerSubmit("feature/unregistered", commitA)).Change.Change
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	r.g.dispatchStepTasks(context.Background())
	var owner string
	if err := r.s.db.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND revision=? AND step='test' AND after_merge=0`, c.ID, c.Revision).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != "/merger" {
		t.Fatalf("test task owner=%q, want /merger before the stop", owner)
	}
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, Configure: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"tests", "merges"} {
		var source string
		if err := r.s.db.QueryRow(`SELECT source FROM graph_edges WHERE target='demo' AND kind=?`, kind).Scan(&source); err != nil {
			t.Fatalf("%s edge: %v", kind, err)
		}
		if source != "/delivery/caller" {
			t.Fatalf("%s owner=%q, want /delivery/caller", kind, source)
		}
	}
	if err := r.s.db.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND revision=? AND step='test' AND after_merge=0`, c.ID, c.Revision).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != "/caller" {
		t.Fatalf("test task owner=%q, want /caller after the stop", owner)
	}
	var history int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM graph_history WHERE node='/delivery/merger' AND attr='tests'`).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if history == 0 {
		t.Fatal("stopping without a registration did not record the edge change in graph history")
	}
}

// Removing one machine's merger stops routing for the whole project, so a
// registration on another machine must not stay behind without routing.
func TestMergerStopClearsEveryMachine(t *testing.T) {
	for _, action := range []string{"stop", "delete"} {
		t.Run(action, func(t *testing.T) {
			r := newDeliveryRig(t)
			demo := protocol.RepoInfo{Root: "/code/demo", Project: "demo"}
			other, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "claude", NativeID: "merger-b", Repo: demo})
			if err != nil {
				t.Fatal(err)
			}
			if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet}, "owner"); err != nil {
				t.Fatal(err)
			}
			if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.b, SessionID: other}, "owner"); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "stop":
				if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, Configure: true}, "owner"); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if _, err := r.s.db.Exec(`DELETE FROM sessions WHERE id=?`, r.sonnet); err != nil {
					t.Fatal(err)
				}
			}
			var n int
			if err := r.s.db.QueryRow(`SELECT count(*) FROM project_mergers WHERE project='demo'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatalf("registrations=%d, want 0 after the project's merger was removed", n)
			}
			a, _, err := r.s.Agent(other)
			if err != nil {
				t.Fatal(err)
			}
			if a.Type == "merger" {
				t.Fatal("other machine's session stayed a merger without routing")
			}
		})
	}
}

// Pruning the merger session returns its task to the caller and tells the
// caller about it.
func TestMergerPruneNotifiesCaller(t *testing.T) {
	r := newDeliveryRig(t)
	r.g.opts.Retention = DefaultRetention()
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet, Configure: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	c := r.submit(r.a, r.workerSubmit("feature/prune", commitA)).Change.Change
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	r.g.dispatchStepTasks(context.Background())
	if err := r.s.SetSessionOffline(r.a, r.sonnet); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET last_seen=0 WHERE id=?`, r.sonnet); err != nil {
		t.Fatal(err)
	}
	if _, err := r.g.prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := r.s.db.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND revision=? AND step='test' AND after_merge=0`, c.ID, c.Revision).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != "/caller" {
		t.Fatalf("test task owner=%q, want /caller after pruning the merger", owner)
	}
	var notices int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_step_messages WHERE change_id=? AND revision=? AND step='test' AND session_id=?`, c.ID, c.Revision, r.caller).Scan(&notices); err != nil {
		t.Fatal(err)
	}
	if notices != 1 {
		t.Fatalf("caller notices=%d, want 1 after pruning the merger", notices)
	}
}
