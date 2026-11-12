package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A merged change still has its after-merge steps to do, so the session that
// took over the conversation owns it. A repair run for a pair that was already
// replaced moves the ownership an earlier version missed, including a claimed
// step and a notice already addressed to the old session, and a further run
// changes nothing else.
func TestReplaceMergedDeliveryRetry(t *testing.T) {
	r := newDeliveryRig(t)
	callerNeedsSharedPipeline(t, r)
	c := mergedChange(t, r, "feature/replace-merged", true)
	// Give the old caller its after-merge task and an undelivered notice.
	r.g.dispatchStepTasks(context.Background())
	var notice string
	if err := r.s.db.QueryRow(`SELECT d.message_id FROM delivery_step_messages d JOIN messages m ON m.id=d.message_id
		WHERE d.change_id=? AND d.session_id=? AND d.after_merge=1 AND m.state IN ('stored','dispatched') LIMIT 1`, c.ID, r.caller).Scan(&notice); err != nil {
		t.Fatalf("the old caller has no undelivered after-merge notice: %v", err)
	}
	if err := r.s.SetSessionOffline(r.a, r.caller); err != nil {
		t.Fatal(err)
	}
	fresh, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "replacement", Repo: protocol.RepoInfo{Root: "/code/demo", Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if moved, err := r.s.ReplaceSession(r.a, r.caller, fresh); err != nil || !moved {
		t.Fatalf("replace: %v %v", moved, err)
	}
	if got := r.get(c.ID).Change.Caller; got != fresh {
		t.Fatalf("caller %s want %s", got, fresh)
	}
	// Simulate a release that marked the session replaced but missed the
	// change ownership, the claim of its step, and the notice.
	if _, err := r.s.db.Exec("UPDATE delivery_changes SET caller=? WHERE id=?", r.caller, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE delivery_step_tasks SET claimed_by=? WHERE change_id=? AND claimed_by=''`, r.caller, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE delivery_step_messages SET session_id=? WHERE change_id=? AND session_id=?`, r.caller, c.ID, fresh); err != nil {
		t.Fatal(err)
	}
	// The repair run moves what the first one missed. It reports that it
	// moved something, so the owner knows the retry was not a no-op.
	moved, err := r.s.ReplaceSession(r.a, r.caller, fresh)
	if err != nil || !moved {
		t.Fatalf("retry: moved=%v err=%v", moved, err)
	}
	if got := r.get(c.ID).Change.Caller; got != fresh {
		t.Fatalf("retry caller %s want %s", got, fresh)
	}
	var claimed, recipient string
	if err := r.s.db.QueryRow(`SELECT claimed_by FROM delivery_step_tasks WHERE change_id=? AND claimed_by<>'' LIMIT 1`, c.ID).Scan(&claimed); err != nil || claimed != fresh {
		t.Fatalf("retry did not move the step claim: %q %v", claimed, err)
	}
	if err := r.s.db.QueryRow(`SELECT session_id FROM delivery_step_messages WHERE change_id=? AND message_id=?`, c.ID, notice).Scan(&recipient); err != nil || recipient != fresh {
		t.Fatalf("retry did not move the step notice: %q %v", recipient, err)
	}
	// A third run for the same pair has nothing left to move and leaves the
	// names and the replaced marker alone.
	before, _, err := r.s.Agent(r.caller)
	if err != nil {
		t.Fatal(err)
	}
	after, _, err := r.s.Agent(fresh)
	if err != nil {
		t.Fatal(err)
	}
	moved, err = r.s.ReplaceSession(r.a, r.caller, fresh)
	if err != nil || moved {
		t.Fatalf("third run: moved=%v err=%v, want no move", moved, err)
	}
	old, _, err := r.s.Agent(r.caller)
	if err != nil {
		t.Fatal(err)
	}
	live, _, err := r.s.Agent(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if old.Profile.Name != before.Profile.Name || old.ReplacedBy != fresh || live.Profile.Name != after.Profile.Name {
		t.Fatalf("a retry changed names or bindings: old=%+v live=%+v", old, live)
	}
	// The new caller can now report the after-merge step the old record held.
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: fresh, ChangeID: c.ID, StepRef: releaseRef, Result: protocol.StepResultPass, Commit: c.MergedCommit}); err != nil {
		t.Fatalf("replacement release: %v", err)
	}
}

// A change that is done or stopped has no steps left to do, and a merged
// change has no before-merge steps left. Dispatching reminders must end those
// tasks and expire the notices they held, even when an earlier state change
// missed them.
func TestDeliveryReminderRetiresObsoleteTasks(t *testing.T) {
	for _, state := range []string{"done", "stopped", "merged"} {
		t.Run(state, func(t *testing.T) {
			r := newDeliveryRig(t)
			callerNeedsSharedPipeline(t, r)
			c := r.submit(r.a, r.workerSubmit("feature/obsolete", commitA)).Change.Change
			// A shared pipeline gives the caller a real before-merge task and
			// a stored step notice.
			r.g.dispatchStepTasks(context.Background())
			var pending, notices int
			if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM delivery_step_tasks WHERE change_id=? AND completed=0`, c.ID).Scan(&pending); err != nil || pending == 0 {
				t.Fatalf("the change has no pending task to retire: %d %v", pending, err)
			}
			if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM delivery_step_messages d JOIN messages m ON m.id=d.message_id
				WHERE d.change_id=? AND m.state IN ('stored','dispatched')`, c.ID).Scan(&notices); err != nil || notices == 0 {
				t.Fatalf("the change has no stored notice to expire: %d %v", notices, err)
			}
			// Past the reminder interval the same task becomes a reminder. It
			// must not outlive the change either.
			r.clk.t = r.clk.t.Add(2*time.Hour + time.Minute)
			r.g.dispatchStepTasks(context.Background())
			r.g.flushStepTasks(r.caller)
			var sent int
			if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE body LIKE '[agent-net step task reminder%'
				AND body LIKE '%'||?||'%'`, c.ID).Scan(&sent); err != nil || sent == 0 {
				t.Fatalf("no reminder was produced for the open change: %d %v", sent, err)
			}
			// A stuck notice is a plain message with its own key, so the
			// change's retirement must reach it too.
			r.clk.t = r.clk.t.Add(13 * time.Hour)
			r.g.dispatchStepTasks(context.Background())
			var stuck int
			if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE sender='agent-net'
				AND state IN ('stored','dispatched') AND idem_key LIKE 'delivery:stuck:'||?||':%'`, c.ID).Scan(&stuck); err != nil || stuck == 0 {
				t.Fatalf("no stuck notice was produced for the open change: %d %v", stuck, err)
			}
			if _, err := r.s.db.Exec("UPDATE delivery_changes SET state=? WHERE id=?", state, c.ID); err != nil {
				t.Fatal(err)
			}
			r.clk.t = r.clk.t.Add(3 * time.Hour)
			r.g.dispatchStepTasks(context.Background())
			if err := r.s.db.QueryRow("SELECT COUNT(*) FROM delivery_step_tasks WHERE change_id=? AND completed=0", c.ID).Scan(&pending); err != nil {
				t.Fatal(err)
			}
			if pending != 0 {
				t.Fatalf("obsolete pending tasks: %d", pending)
			}
			if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM messages m JOIN delivery_step_messages d ON d.message_id=m.id
				WHERE d.change_id=? AND m.state IN ('stored','dispatched')`, c.ID).Scan(&notices); err != nil {
				t.Fatal(err)
			}
			if notices != 0 {
				t.Fatalf("obsolete deliverable notices: %d", notices)
			}
			var reminders int
			if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE body LIKE '[agent-net step task reminder%'
				AND state IN ('stored','dispatched') AND body LIKE '%'||?||'%'`, c.ID).Scan(&reminders); err != nil {
				t.Fatal(err)
			}
			if reminders != 0 {
				t.Fatalf("obsolete reminders: %d", reminders)
			}
			if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE sender='agent-net'
				AND state IN ('stored','dispatched') AND idem_key LIKE 'delivery:stuck:'||?||':%'`, c.ID).Scan(&stuck); err != nil {
				t.Fatal(err)
			}
			if stuck != 0 {
				t.Fatalf("obsolete stuck notices: %d", stuck)
			}
		})
	}
}
