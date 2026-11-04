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
	if err := r.s.SetMerger(q, "cto"); err == nil {
		t.Fatal("unapproved agent registered merger")
	}
	if err := r.s.SetMerger(q, "owner"); err != nil {
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
