package gateway

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func nudgeMessages(t *testing.T, r *deliveryRig) []protocol.Message {
	t.Helper()
	ms, err := r.s.Pending(r.a, true)
	if err != nil {
		t.Fatal(err)
	}
	var out []protocol.Message
	for _, m := range ms {
		if m.From == "agent-net" && m.To == r.caller {
			out = append(out, m)
		}
	}
	return out
}

func TestStepRemindersPullForwardOneHour(t *testing.T) {
	r := setupMergerRig(t)
	first := r.submit(r.a, r.workerSubmit("feature/first", commitA)).Change.Change.ID
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	r.clk.t = r.clk.t.Add(20 * time.Minute)
	second := r.submit(r.a, r.workerSubmit("feature/second", commitA)).Change.Change.ID
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	if _, err := r.s.db.Exec(`UPDATE messages SET state='delivered' WHERE recipient=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(60 * time.Minute)
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	if got := nudgeMessages(t, r); len(got) != 0 {
		t.Fatalf("future candidates alone triggered a nudge: %+v", got)
	}
	r.clk.t = r.clk.t.Add(41 * time.Minute)
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	got := nudgeMessages(t, r)
	if len(got) != 1 || !strings.Contains(got[0].Body, first) || !strings.Contains(got[0].Body, second) {
		t.Fatalf("want one message with both reminders: %+v", got)
	}
	if _, err := r.s.db.Exec(`UPDATE messages SET state='delivered' WHERE recipient=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(20 * time.Minute)
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	if got := nudgeMessages(t, r); len(got) != 0 {
		t.Fatalf("pulled-forward reminder repeated: %+v", got)
	}
}

func TestStuckAndStepReminderShareMessage(t *testing.T) {
	r := setupMergerRig(t)
	first := r.submit(r.a, r.workerSubmit("feature/remind", commitA)).Change.Change.ID
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	second := r.submit(r.a, r.workerSubmit("feature/stuck", commitA)).Change.Change.ID
	if _, err := r.s.db.Exec(`UPDATE delivery_step_tasks SET owner_node='absent',created_at=? WHERE change_id=?`, r.clk.t.Add(-10*time.Hour).Unix(), second); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE messages SET state='delivered' WHERE recipient=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(2*time.Hour + time.Minute)
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	got := nudgeMessages(t, r)
	if len(got) != 1 || !strings.Contains(got[0].Body, first) || !strings.Contains(got[0].Body, second) || !strings.Contains(got[0].Body, "nobody claimed it") {
		t.Fatalf("want reminder and stuck notice in one message: %+v", got)
	}
}

func TestReplyReminderPullsForwardStepNudge(t *testing.T) {
	r := setupMergerRig(t)
	change := r.submit(r.a, r.workerSubmit("feature/reply-trigger", commitA)).Change.Change.ID
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	if _, err := r.s.db.Exec(`UPDATE messages SET state='delivered' WHERE recipient=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(100 * time.Minute)
	req, _, err := send(r.s, r.sonnet, r.caller, "please reply", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.s.db.Exec(`UPDATE sessions SET status='idle',last_active=? WHERE id=?`, r.clk.t.UnixNano(), r.caller); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(3 * time.Minute)
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	got := nudgeMessages(t, r)
	if len(got) != 1 || !strings.Contains(got[0].Body, change) || !strings.Contains(got[0].Body, req.ID) {
		t.Fatalf("reply trigger failed to gather step: %+v", got)
	}
	if _, _, err = send(r.s, r.caller, r.sonnet, "done", req.ID); err != nil {
		t.Fatal(err)
	}
	// The queued envelope retains its step when the request has been answered.
	got = nudgeMessages(t, r)
	if len(got) != 1 || strings.Contains(got[0].Body, req.ID) || !strings.Contains(got[0].Body, change) {
		t.Fatalf("queued parts not rechecked independently: %+v", got)
	}
}

func TestStepReminderPullsForwardYoungReply(t *testing.T) {
	r := setupMergerRig(t)
	change := r.submit(r.a, r.workerSubmit("feature/step-trigger", commitA)).Change.Change.ID
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	if _, err := r.s.db.Exec(`UPDATE messages SET state='delivered' WHERE recipient=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(2*time.Hour + time.Minute)
	req, _, err := send(r.s, r.sonnet, r.caller, "please reply", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.s.db.Exec(`UPDATE sessions SET status='idle',last_active=? WHERE id=?`, r.clk.t.UnixNano(), r.caller); err != nil {
		t.Fatal(err)
	}
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	got := nudgeMessages(t, r)
	if len(got) != 1 || !strings.Contains(got[0].Body, change) || !strings.Contains(got[0].Body, req.ID) {
		t.Fatalf("young reply not pulled forward: %+v", got)
	}
	if _, err = r.s.db.Exec(`UPDATE messages SET state='delivered' WHERE recipient=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(3 * time.Minute)
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	if got := nudgeMessages(t, r); len(got) != 0 {
		t.Fatalf("young reply repeated: %+v", got)
	}
}

func TestVerifyReminderPulledForwardAdvancesCadence(t *testing.T) {
	r := newDeliveryRig(t)
	callerNeedsSharedPipeline(t, r)
	c := mergedChange(t, r, "feature/verify-lookahead", true)
	if _, err := r.s.db.Exec(`UPDATE delivery_step_tasks SET completed=1 WHERE step<>'verify'`); err != nil {
		t.Fatal(err)
	}
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	start := r.clk.t
	r.clk.t = start.Add(10 * time.Minute)
	req, _, err := send(r.s, r.sonnet, r.caller, "please reply", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.s.db.Exec(`UPDATE sessions SET status='idle',last_active=? WHERE id=?`, start.UnixNano(), r.caller); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(3 * time.Minute)
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	var sent, covered int64
	if err := r.s.db.QueryRow(`SELECT last_notice_at,last_notice_due_at FROM delivery_step_tasks WHERE change_id=? AND step='verify' AND after_merge=1`, c.ID).Scan(&sent, &covered); err != nil {
		t.Fatal(err)
	}
	if sent != r.clk.t.UnixNano() || covered != start.Add(30*time.Minute).UnixNano() {
		t.Fatalf("sent=%d covered=%d", sent, covered)
	}
	if _, _, err = send(r.s, r.caller, r.sonnet, "done", req.ID); err != nil {
		t.Fatal(err)
	}
	r.clk.t = start.Add(31 * time.Minute)
	target := nodeTaskTarget{id: c.ID, revision: c.Revision, ref: protocol.StepRef{Step: protocol.StepVerify, AfterMerge: true}, session: r.caller}
	recorded, err := r.s.stepMessageRecorded(target, r.s.stepReminderCutoff())
	if err != nil || !recorded {
		t.Fatalf("pulled verify due again at 30m: recorded=%v %v", recorded, err)
	}
	r.clk.t = start.Add(2 * time.Hour)
	recorded, err = r.s.stepMessageRecorded(target, r.s.stepReminderCutoff())
	if err != nil || recorded {
		t.Fatalf("next verify slot changed: recorded=%v %v", recorded, err)
	}
}

func TestRemindOfflineCountWithoutDueNudge(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := send(s, a, b, "please reply", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=0,status='idle',last_active=? WHERE id=?`, c.t.UnixNano(), b); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(3 * time.Minute)
	got, err := s.remindPendingBefore(c.t.Add(nudgeLookahead), map[string]bool{})
	if err != nil || len(got) != 0 {
		t.Fatalf("offline reminders: %+v %v", got, err)
	}
	counts := protocol.SessionCounts{}
	if err := s.reminderCounts(&counts); err != nil {
		t.Fatal(err)
	}
	if counts.PendingRemindersOffline != 1 {
		t.Fatalf("offline count=%d want=1", counts.PendingRemindersOffline)
	}
}

func TestNudgeWaitingStepNotPulledForward(t *testing.T) {
	r, c, ref := waitingRig(t)
	reportWaiting(t, r, c, ref, "")
	var waitingAt, lastNotice int64
	if err := r.s.db.QueryRow(`SELECT waiting_at,last_notice_at FROM delivery_step_tasks WHERE change_id=? AND step='verify'`, c.ID).Scan(&waitingAt, &lastNotice); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(10 * time.Minute)
	req, _, err := send(r.s, r.sonnet, r.caller, "please reply", "")
	if err != nil {
		t.Fatal(err)
	}
	idleSince(t, r.s, r.clk, r.caller, 3*time.Minute)
	r.clk.t = r.clk.t.Add(3 * time.Minute)
	waitingDispatch(r)
	got := nudgeMessages(t, r)
	if len(got) != 1 || !strings.Contains(got[0].Body, req.ID) || strings.Contains(got[0].Body, c.ID) {
		t.Fatalf("waiting step pulled forward: %+v", got)
	}
	var at, notice int64
	if err := r.s.db.QueryRow(`SELECT waiting_at,last_notice_at FROM delivery_step_tasks WHERE change_id=? AND step='verify'`, c.ID).Scan(&at, &notice); err != nil {
		t.Fatal(err)
	}
	if at != waitingAt || notice != lastNotice {
		t.Fatalf("waiting state changed: at=%d/%d notice=%d/%d", at, waitingAt, notice, lastNotice)
	}
}

func TestNudgeEnvelopeExcludesPausedParts(t *testing.T) {
	for _, kind := range []string{"step_reminder", "reply_reminder"} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/after=%v", kind, after), func(t *testing.T) {
				r := setupMergerRig(t)
				if err := r.s.SetChief(protocol.ChiefSetRequest{SessionID: r.sonnet}, "owner"); err != nil {
					t.Fatal(err)
				}
				nudgeAgent(t, r.s, r.caller, "envelope-agent")
				change := r.submit(r.a, r.workerSubmit("feature/paused-envelope", commitA)).Change.Change.ID
				r.g.flushStepTasks(r.caller)
				if _, err := r.s.db.Exec(`UPDATE messages SET state='delivered' WHERE recipient=?`, r.caller); err != nil {
					t.Fatal(err)
				}
				req, _, err := send(r.s, r.sonnet, r.caller, "please reply", "")
				if err != nil {
					t.Fatal(err)
				}
				idleSince(t, r.s, r.clk, r.caller, 3*time.Minute)
				r.clk.t = r.clk.t.Add(2*time.Hour + time.Minute)
				apply := func() { nudgeApply(t, r.s, r.sonnet, map[string]any{"kind": kind, "agent": "envelope-agent"}) }
				if !after {
					apply()
				}
				waitingDispatch(r)
				if after {
					got := nudgeMessages(t, r)
					if len(got) != 1 || len(got[0].Batch) != 2 {
						t.Fatalf("expected envelope before pause: %+v", got)
					}
					apply()
				}
				got := nudgeMessages(t, r)
				if len(got) != 1 {
					t.Fatalf("want one remaining nudge: %+v", got)
				}
				held, allowed := change, req.ID
				if kind == "reply_reminder" {
					held, allowed = req.ID, change
				}
				if strings.Contains(got[0].Body, held) || !strings.Contains(got[0].Body, allowed) {
					t.Fatalf("paused part included: %s", got[0].Body)
				}
			})
		}
	}
}

func TestNudgePausedReplyDoesNotTriggerLookahead(t *testing.T) {
	r := setupMergerRig(t)
	if err := r.s.SetChief(protocol.ChiefSetRequest{SessionID: r.sonnet}, "owner"); err != nil {
		t.Fatal(err)
	}
	nudgeAgent(t, r.s, r.caller, "lookahead-agent")
	r.submit(r.a, r.workerSubmit("feature/paused-trigger", commitA))
	r.g.flushStepTasks(r.caller)
	if _, err := r.s.db.Exec(`UPDATE messages SET state='delivered' WHERE recipient=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(100 * time.Minute)
	if _, _, err := send(r.s, r.sonnet, r.caller, "please reply", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, r.s, r.clk, r.caller, 3*time.Minute)
	r.clk.t = r.clk.t.Add(3 * time.Minute)
	nudgeApply(t, r.s, r.sonnet, map[string]any{"kind": "reply_reminder", "agent": "lookahead-agent"})
	waitingDispatch(r)
	if got := nudgeMessages(t, r); len(got) != 0 {
		t.Fatalf("paused request triggered future step reminder: %+v", got)
	}
}
