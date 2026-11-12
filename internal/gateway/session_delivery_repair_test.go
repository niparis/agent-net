package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestReplaceMergedDeliveryRetry(t *testing.T) {
	r := newDeliveryRig(t)
	callerNeedsSharedPipeline(t, r)
	c := mergedChange(t, r, "feature/replace-merged", true)
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
	// Simulate a prior release that marked the session replaced but missed merged ownership.
	if _, err := r.s.db.Exec("UPDATE delivery_changes SET caller=? WHERE id=?", r.caller, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.ReplaceSession(r.a, r.caller, fresh); err != nil {
		t.Fatal(err)
	}
	if got := r.get(c.ID).Change.Caller; got != fresh {
		t.Fatalf("retry caller %s want %s", got, fresh)
	}
	if _, err := r.s.ReplaceSession(r.a, r.caller, fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: fresh, ChangeID: c.ID, StepRef: releaseRef, Result: protocol.StepResultPass, Commit: c.MergedCommit}); err != nil {
		t.Fatalf("replacement release: %v", err)
	}
}

func TestDeliveryReminderRetiresObsoleteTasks(t *testing.T) {
	for _, state := range []string{"done", "stopped", "merged"} {
		t.Run(state, func(t *testing.T) {
			r := newDeliveryRig(t)
			c := r.submit(r.a, r.workerSubmit("feature/obsolete", commitA)).Change.Change
			r.g.dispatchStepTasks(context.Background())
			if _, err := r.s.db.Exec("UPDATE delivery_changes SET state=? WHERE id=?", state, c.ID); err != nil {
				t.Fatal(err)
			}
			r.clk.t = r.clk.t.Add(3 * time.Hour)
			r.g.dispatchStepTasks(context.Background())
			var pending int
			if err := r.s.db.QueryRow("SELECT COUNT(*) FROM delivery_step_tasks WHERE change_id=? AND after_merge=0 AND completed=0", c.ID).Scan(&pending); err != nil {
				t.Fatal(err)
			}
			if pending != 0 {
				t.Fatalf("obsolete pending tasks: %d", pending)
			}
			var notices int
			if err := r.s.db.QueryRow("SELECT COUNT(*) FROM messages m JOIN delivery_step_messages d ON d.message_id=m.id WHERE d.change_id=? AND d.after_merge=0 AND m.state IN ('stored','dispatched')", c.ID).Scan(&notices); err != nil {
				t.Fatal(err)
			}
			if notices != 0 {
				t.Fatalf("obsolete deliverable notices: %d", notices)
			}
		})
	}
}
