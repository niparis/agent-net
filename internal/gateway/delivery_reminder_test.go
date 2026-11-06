package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func setupMergerRig(t *testing.T) *deliveryRig {
	t.Helper()
	r := newDeliveryRig(t)
	q := protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet}
	if err := r.s.SetMerger(q, "owner"); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"test", "merge"} {
		if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: step, Node: "@merger"}, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestDeliveryStepReminder(t *testing.T) {
	r := setupMergerRig(t)

	q := r.workerSubmit("feature/remind", commitA)
	res := r.submit(r.a, q)
	cid := res.Change.Change.ID

	// Initial dispatch should send 1 task message.
	r.g.dispatchStepTasks(context.Background())

	var count int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE recipient=? AND type=?`, r.caller, protocol.TypeNotification).Scan(&count); err != nil || count != 1 {
		t.Fatalf("initial message count=%d want=1 err=%v", count, err)
	}

	var firstBody string
	if err := r.s.db.QueryRow(`SELECT body FROM messages WHERE recipient=? ORDER BY created_at DESC LIMIT 1`, r.caller).Scan(&firstBody); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(firstBody, "[agent-net step task]\n") {
		t.Fatalf("expected initial prefix [agent-net step task], got: %s", firstBody)
	}

	// Advance clock by 1 hour (default reminder is 2h): should NOT send reminder yet.
	r.clk.t = r.clk.t.Add(1 * time.Hour)
	r.g.dispatchStepTasks(context.Background())

	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE recipient=? AND type=?`, r.caller, protocol.TypeNotification).Scan(&count); err != nil || count != 1 {
		t.Fatalf("message count after 1h=%d want=1 err=%v", count, err)
	}

	// Advance clock by another 1.5 hours (total 2.5h > 2h): reminder should be sent.
	r.clk.t = r.clk.t.Add(90 * time.Minute)
	r.g.dispatchStepTasks(context.Background())

	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE recipient=? AND type=?`, r.caller, protocol.TypeNotification).Scan(&count); err != nil || count != 2 {
		t.Fatalf("message count after 2.5h=%d want=2 err=%v", count, err)
	}

	var reminderBody string
	if err := r.s.db.QueryRow(`SELECT body FROM messages WHERE recipient=? ORDER BY created_at DESC LIMIT 1`, r.caller).Scan(&reminderBody); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(reminderBody, "[agent-net step task reminder]\n") {
		t.Fatalf("expected reminder prefix [agent-net step task reminder], got: %s", reminderBody)
	}
	if !strings.Contains(reminderBody, "ready since") || !strings.Contains(reminderBody, ", not claimed") {
		t.Fatalf("expected 'ready since' and ', not claimed' in reminder, got: %s", reminderBody)
	}

	// Advance clock by 10 minutes: at most one reminder per interval, so no duplicate.
	r.clk.t = r.clk.t.Add(10 * time.Minute)
	r.g.dispatchStepTasks(context.Background())

	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE recipient=? AND type=?`, r.caller, protocol.TypeNotification).Scan(&count); err != nil || count != 2 {
		t.Fatalf("message count after 10m=%d want=2 err=%v", count, err)
	}

	// Claim the task: a claimed task must NOT be re-sent.
	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: r.caller, ChangeID: cid, StepRef: stepAccept, Commit: commitA}); err != nil {
		t.Fatal(err)
	}

	// Advance clock by another 3 hours: claimed task must not be re-sent.
	r.clk.t = r.clk.t.Add(3 * time.Hour)
	r.g.dispatchStepTasks(context.Background())

	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE recipient=? AND type=?`, r.caller, protocol.TypeNotification).Scan(&count); err != nil || count != 2 {
		t.Fatalf("message count after claim and 3h=%d want=2 err=%v", count, err)
	}
}

func TestDeliveryStepStuckNotice(t *testing.T) {
	r := setupMergerRig(t)

	q := r.workerSubmit("feature/stuck", commitA)
	q.Title = "Fix login crash"
	res := r.submit(r.a, q)
	cid := res.Change.Change.ID

	r.g.dispatchStepTasks(context.Background())

	// Advance clock by 11 hours (< 12h default step_stuck): no stuck notice yet.
	r.clk.t = r.clk.t.Add(11 * time.Hour)
	r.g.dispatchStepTasks(context.Background())

	var stuckCount int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE recipient=? AND body LIKE '%has waited%nobody claimed it%'`, r.caller).Scan(&stuckCount); err != nil || stuckCount != 0 {
		t.Fatalf("stuck notice count after 11h=%d want=0 err=%v", stuckCount, err)
	}

	// Advance clock by 2 hours (total 13h > 12h): stuck notice should be sent to caller.
	r.clk.t = r.clk.t.Add(2 * time.Hour)
	r.g.dispatchStepTasks(context.Background())

	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE recipient=? AND body LIKE '%has waited%nobody claimed it%'`, r.caller).Scan(&stuckCount); err != nil || stuckCount != 1 {
		t.Fatalf("stuck notice count after 13h=%d want=1 err=%v", stuckCount, err)
	}

	var stuckBody string
	if err := r.s.db.QueryRow(`SELECT body FROM messages WHERE recipient=? AND body LIKE '%has waited%nobody claimed it%'`, r.caller).Scan(&stuckBody); err != nil {
		t.Fatal(err)
	}
	wantBody := "Fix login crash (" + cid + "): accept has waited 13h for caller; nobody claimed it."
	if stuckBody != wantBody {
		t.Fatalf("stuck notice body=%q want=%q", stuckBody, wantBody)
	}

	// Dispatch again: exactly one stuck notice must be sent per change, revision and step.
	r.clk.t = r.clk.t.Add(2 * time.Hour)
	r.g.dispatchStepTasks(context.Background())

	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE recipient=? AND body LIKE '%has waited%nobody claimed it%'`, r.caller).Scan(&stuckCount); err != nil || stuckCount != 1 {
		t.Fatalf("stuck notice count on second dispatch=%d want=1 err=%v", stuckCount, err)
	}

	// Claimed task must NOT get a stuck notice.
	q2 := r.workerSubmit("feature/claimed", commitB)
	q2.Title = "Claimed feature"
	res2 := r.submit(r.a, q2)
	cid2 := res2.Change.Change.ID
	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: r.caller, ChangeID: cid2, StepRef: stepAccept, Commit: commitB}); err != nil {
		t.Fatal(err)
	}

	r.clk.t = r.clk.t.Add(14 * time.Hour)
	r.g.dispatchStepTasks(context.Background())

	var claimedStuckCount int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE recipient=? AND body LIKE '%Claimed feature%'`, r.caller).Scan(&claimedStuckCount); err != nil || claimedStuckCount != 0 {
		t.Fatalf("claimed task stuck notice count=%d want=0 err=%v", claimedStuckCount, err)
	}
}

func TestDeliveryReminderAndStuckDisabledByZero(t *testing.T) {
	r := setupMergerRig(t)

	// Set both delivery.step_reminder and delivery.step_stuck to 0s
	if _, err := r.s.WriteSettings([]SettingsChange{
		{Key: "delivery.step_reminder", Level: "network", Scope: "network", Value: json.RawMessage(`"0s"`)},
		{Key: "delivery.step_stuck", Level: "network", Scope: "network", Value: json.RawMessage(`"0s"`)},
	}, "owner", "disable"); err != nil {
		t.Fatal(err)
	}

	q := r.workerSubmit("feature/disabled", commitA)
	q.Title = "Zero disable"
	r.submit(r.a, q)

	r.g.dispatchStepTasks(context.Background())

	var initialCount int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE recipient=?`, r.caller).Scan(&initialCount); err != nil || initialCount != 1 {
		t.Fatalf("initial count=%d want=1 err=%v", initialCount, err)
	}

	// Advance clock by 48 hours: neither reminders nor stuck notices should be sent.
	r.clk.t = r.clk.t.Add(48 * time.Hour)
	r.g.dispatchStepTasks(context.Background())

	var afterCount int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE recipient=?`, r.caller).Scan(&afterCount); err != nil || afterCount != 1 {
		t.Fatalf("after count=%d want=1 (0 should disable both reminders and stuck notices) err=%v", afterCount, err)
	}
}
