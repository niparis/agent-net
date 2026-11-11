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

// recentStepMessage records a delivered step notice for the caller. It makes
// the session look recently active, so the next ready tasks are batched behind
// the quiet window instead of being sent immediately.
func recentStepMessage(t *testing.T, r *deliveryRig, change string, ref protocol.StepRef) {
	t.Helper()
	now := r.s.now().UTC()
	m := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"),
		From: "agent-net", FromName: "agent-net", FromHarness: "agent-net", To: r.caller,
		Type: protocol.TypeNotification, Body: "recent step activity", IdempotencyKey: protocol.NewID("step"),
		CreatedAt: now, ExpiresAt: now.Add(protocol.DefaultTTL), State: protocol.StateStored}
	if _, err := r.s.db.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`INSERT INTO delivery_step_messages(change_id,revision,step,for_step,after_merge,session_id,message_id)
		VALUES(?,?,?,?,?,?,?)`, change, 1, ref.Step, ref.For, boolInt(ref.AfterMerge), r.caller, m.ID); err != nil {
		t.Fatal(err)
	}
}

func taskBodies(t *testing.T, r *deliveryRig) []string {
	t.Helper()
	rows, err := r.s.db.Query(`SELECT body FROM messages WHERE recipient=? AND sender='agent-net'
		AND (body LIKE '[agent-net step task%') ORDER BY rowid`, r.caller)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatal(err)
		}
		out = append(out, body)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Two after-merge steps that become ready together get one task message. While
// the reminder interval has not elapsed, further dispatch passes must send
// nothing: no step named in the first message may be sent again.
func TestDeliveryStepPairReadyTogetherNoEarlyReminder(t *testing.T) {
	r := newDeliveryRig(t)
	callerNeedsSharedPipeline(t, r)
	c := mergedChange(t, r, "feature/pair-reminder", true)
	// A notice from the merge phase makes the caller look recently active, so
	// the pair waits for the quiet window like it did on the live gateway.
	recentStepMessage(t, r, c.ID, protocol.StepRef{Step: protocol.StepMerge})

	r.g.dispatchStepTasks(context.Background())

	// A dispatch pass can overlap the quiet-window flush: it selects the tasks
	// before the first message has recorded its rows. Run that pass right after
	// the batch leaves the map and before the message is stored, as happened on
	// the live gateway.
	r.g.onStepBatchFlushed = func(string) {
		r.g.onStepBatchFlushed = nil
		r.g.dispatchStepTasks(context.Background())
	}
	r.g.flushStepTasks(r.caller)
	// The overlapping pass left a batch behind. Flush it as its timer would.
	r.g.flushStepTasks(r.caller)

	bodies := taskBodies(t, r)
	if len(bodies) != 1 {
		t.Fatalf("caller got %d task messages, want 1: %v", len(bodies), bodies)
	}
	if !strings.Contains(bodies[0], "steps: release, verify (after merge)") {
		t.Fatalf("pair line missing from:\n%s", bodies[0])
	}

	// Every step named in the message has its delivery_step_messages row.
	var rows int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_step_messages WHERE change_id=? AND session_id=?`,
		c.ID, r.caller).Scan(&rows); err != nil || rows != 3 {
		t.Fatalf("step message rows=%d want=3 (recent + release + verify) err=%v", rows, err)
	}

	// Dispatch several times inside the reminder interval. Nothing new may go out.
	for i := 0; i < 3; i++ {
		r.clk.t = r.clk.t.Add(3 * time.Second)
		r.g.dispatchStepTasks(context.Background())
		r.g.flushStepTasks(r.caller)
	}
	if got := taskBodies(t, r); len(got) != 1 {
		t.Fatalf("caller got %d task messages inside the reminder interval, want 1: %v", len(got), got)
	}

	// Past delivery.step_reminder exactly one reminder goes out.
	r.clk.t = r.clk.t.Add(2*time.Hour + time.Minute)
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	bodies = taskBodies(t, r)
	if len(bodies) != 2 {
		t.Fatalf("caller got %d task messages after the reminder interval, want 2: %v", len(bodies), bodies)
	}
	if !strings.HasPrefix(bodies[1], "[agent-net step task reminder]") {
		t.Fatalf("second message is not a reminder:\n%s", bodies[1])
	}

	// A further dispatch must not repeat the reminder.
	r.clk.t = r.clk.t.Add(time.Minute)
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	if got := taskBodies(t, r); len(got) != 2 {
		t.Fatalf("caller got %d task messages after a repeated dispatch, want 2: %v", len(got), got)
	}
}

func TestDeliveryStepStuckNotice(t *testing.T) {
	r := setupMergerRig(t)

	q := r.workerSubmit("feature/stuck", commitA)
	q.Title = "Fix login crash"
	res := r.submit(r.a, q)
	cid := res.Change.Change.ID

	r.g.dispatchStepTasks(context.Background())

	// At 11h a due step reminder pulls the 12h stuck notice forward.
	r.clk.t = r.clk.t.Add(11 * time.Hour)
	r.g.dispatchStepTasks(context.Background())

	var stuckCount int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE recipient=? AND idem_key LIKE 'delivery:stuck:%' AND body LIKE '%has waited%nobody claimed it%'`, r.caller).Scan(&stuckCount); err != nil || stuckCount != 1 {
		t.Fatalf("stuck notice count after 11h=%d want=1 err=%v", stuckCount, err)
	}

	// Advance clock by 2 hours (total 13h > 12h): stuck notice should be sent to caller.
	r.clk.t = r.clk.t.Add(2 * time.Hour)
	r.g.dispatchStepTasks(context.Background())

	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE recipient=? AND idem_key LIKE 'delivery:stuck:%' AND body LIKE '%has waited%nobody claimed it%'`, r.caller).Scan(&stuckCount); err != nil || stuckCount != 1 {
		t.Fatalf("stuck notice count after 13h=%d want=1 err=%v", stuckCount, err)
	}

	var stuckBody string
	if err := r.s.db.QueryRow(`SELECT body FROM messages WHERE recipient=? AND idem_key LIKE 'delivery:stuck:%' AND body LIKE '%has waited%nobody claimed it%'`, r.caller).Scan(&stuckBody); err != nil {
		t.Fatal(err)
	}
	wantBody := "Fix login crash (" + cid + "): accept has waited 11h for caller; nobody claimed it."
	if stuckBody != wantBody {
		t.Fatalf("stuck notice body=%q want=%q", stuckBody, wantBody)
	}

	// Dispatch again: exactly one stuck notice must be sent per change, revision and step.
	r.clk.t = r.clk.t.Add(2 * time.Hour)
	r.g.dispatchStepTasks(context.Background())

	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE recipient=? AND idem_key LIKE 'delivery:stuck:%' AND body LIKE '%has waited%nobody claimed it%'`, r.caller).Scan(&stuckCount); err != nil || stuckCount != 1 {
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

func TestVerifyReminderBackoff(t *testing.T) {
	r := newDeliveryRig(t)
	callerNeedsSharedPipeline(t, r)
	c := mergedChange(t, r, "feature/backoff", true)
	ref := protocol.StepRef{Step: protocol.StepVerify, AfterMerge: true}
	target := nodeTaskTarget{id: c.ID, revision: c.Revision, ref: ref, session: r.caller}
	r.g.dispatchStepTasks(context.Background())
	start := r.clk.t
	// Reopen the store after the first reminder; the next dispatch keeps its schedule.
	for _, minutes := range []int{29, 30, 31, 119, 120, 121, 479, 480, 481, 1439, 1440, 1441, 2879, 2880} {
		r.clk.t = start.Add(time.Duration(minutes) * time.Minute)
		got, err := r.s.stepMessageRecorded(target, r.s.stepReminderCutoff())
		due := minutes == 30 || minutes == 120 || minutes == 480 || minutes == 1440 || minutes == 2880
		if err != nil || got == due {
			t.Fatalf("at %dm recorded=%v due=%v err=%v", minutes, got, due, err)
		}
		if due {
			r.g.dispatchStepTasks(context.Background())
			r.g.flushStepTasks(r.caller)
			if minutes == 30 {
				reopened, err := OpenStore(r.s.path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { reopened.Close() })
				reopened.now = r.s.now
				r.s = reopened
				r.g.store = reopened
			}
		}
	}
}

func TestVerifyStuckNoticeSurvivesPruning(t *testing.T) {
	r := newDeliveryRig(t)
	callerNeedsSharedPipeline(t, r)
	mergedChange(t, r, "feature/stuck-verify", true)
	if _, err := r.s.db.Exec("UPDATE delivery_step_tasks SET completed=1 WHERE step<>'verify'"); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(12 * time.Hour)
	r.g.checkStuckStepTasks(context.Background(), 12*time.Hour)
	var count int
	if err := r.s.db.QueryRow("SELECT count(*) FROM messages WHERE idem_key LIKE 'delivery:stuck:%' AND body LIKE '%nobody claimed%'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("first notice=%d err=%v", count, err)
	}
	if _, err := r.s.db.Exec("DELETE FROM messages"); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(24 * time.Hour)
	r.g.checkStuckStepTasks(context.Background(), 12*time.Hour)
	if err := r.s.db.QueryRow("SELECT count(*) FROM messages WHERE idem_key LIKE 'delivery:stuck:%' AND body LIKE '%nobody claimed%'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("repeated notice=%d err=%v", count, err)
	}
}
