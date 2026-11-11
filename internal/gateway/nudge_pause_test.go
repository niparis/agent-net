package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func nudgeCall(t *testing.T, s *Store, caller, frame string, args map[string]any) (map[string]any, error) {
	t.Helper()
	node, err := s.SessionNode(caller)
	if err != nil {
		t.Fatal(err)
	}
	args["session_id"] = caller
	raw, _ := json.Marshal(args)
	out, err := (&Gateway{store: s}).handle(context.Background(), &nodeConn{id: node}, frame, raw)
	data, _ := json.Marshal(out)
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	return result, err
}
func nudgeApply(t *testing.T, s *Store, chief string, args map[string]any) string {
	t.Helper()
	args["mode"] = "apply"
	out, err := nudgeCall(t, s, chief, "nudge.pause", args)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("no pause id: %v", out)
	}
	return id
}
func nudgeAgent(t *testing.T, s *Store, session, agent string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO agents(id,display_name,created_at,updated_at) VALUES(?,?,0,0)`, agent, agent+" name"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET agent_id=? WHERE id=?`, agent, session); err != nil {
		t.Fatal(err)
	}
}
func nudgeSetup(t *testing.T) (*Store, *clock, string, string) {
	t.Helper()
	s, c, chief, target, _ := chiefRig(t)
	nudgeAgent(t, s, chief, "chief-agent")
	nudgeAgent(t, s, target, "target-agent")
	return s, c, chief, target
}
func TestNudgePauseIdleAgent(t *testing.T) {
	s, c, chief, target := nudgeSetup(t)
	nudgeApply(t, s, chief, map[string]any{"kind": "idle_check", "agent": "target-agent name"})
	for _, id := range []string{chief, target} {
		if _, err := s.db.Exec(`INSERT INTO profiles(session_id,task,updated_at) VALUES(?,'work',0) ON CONFLICT(session_id) DO UPDATE SET task='work'`, id); err != nil {
			t.Fatal(err)
		}
		idleSince(t, s, c, id, 21*time.Minute)
	}
	got := checks(t, s)
	if len(got) != 1 || got[0].To != chief {
		t.Fatalf("checks=%+v", got)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM session_checks WHERE session_id=?`, target).Scan(&n); err != nil || n != 0 {
		t.Fatalf("paused bookkeeping=%d: %v", n, err)
	}
}
func nudgeStepRig(t *testing.T) (*deliveryRig, string) {
	t.Helper()
	r := setupMergerRig(t)
	if err := r.s.SetChief(protocol.ChiefSetRequest{SessionID: r.sonnet}, "owner"); err != nil {
		t.Fatal(err)
	}
	nudgeAgent(t, r.s, r.caller, "step-agent")
	if _, err := r.s.db.Exec(`INSERT INTO teams(id,name,created_at,updated_at) VALUES('forge','Forge',0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`INSERT INTO team_members(member_kind,member_id,team_id) VALUES('agent','step-agent','forge')`); err != nil {
		t.Fatal(err)
	}
	id := nudgeApply(t, r.s, r.sonnet, map[string]any{"kind": "step_reminder", "team": "Forge"})
	r.submit(r.a, r.workerSubmit("feature/nudge", commitA))
	return r, id
}
func TestNudgePauseTeamStepReminder(t *testing.T) {
	r, _ := nudgeStepRig(t)
	r.g.dispatchStepTasks(context.Background())
	if bodies := taskBodies(t, r); len(bodies) != 1 || !strings.HasPrefix(bodies[0], "[agent-net step task]\n") {
		t.Fatalf("first notice=%v", bodies)
	}
	r.clk.t = r.clk.t.Add(7 * time.Hour)
	r.g.dispatchStepTasks(context.Background())
	if bodies := taskBodies(t, r); len(bodies) != 1 {
		t.Fatalf("paused reminders=%v", bodies)
	}
}
func TestNudgeResumeNoBacklog(t *testing.T) {
	r, id := nudgeStepRig(t)
	r.g.dispatchStepTasks(context.Background())
	for range 3 {
		r.clk.t = r.clk.t.Add(3 * time.Hour)
		r.g.dispatchStepTasks(context.Background())
	}
	if _, err := nudgeCall(t, r.s, r.sonnet, "nudge.resume", map[string]any{"pause_id": id}); err != nil {
		t.Fatal(err)
	}
	if bodies := taskBodies(t, r); len(bodies) != 1 {
		t.Fatalf("resume delivered backlog=%v", bodies)
	}
	r.g.dispatchStepTasks(context.Background())
	if bodies := taskBodies(t, r); len(bodies) != 2 {
		t.Fatalf("next due reminder=%v", bodies)
	}
	r.g.dispatchStepTasks(context.Background())
	if bodies := taskBodies(t, r); len(bodies) != 2 {
		t.Fatalf("extra backlog=%v", bodies)
	}
}
func TestNudgePauseExpiry(t *testing.T) {
	for _, until := range []string{"time", "provider_reset", "cap"} {
		t.Run(until, func(t *testing.T) {
			s, c, chief, target := nudgeSetup(t)
			expiry := "provider_reset"
			if until == "time" {
				expiry = c.t.Add(time.Hour).Format("2006-01-02T15:04Z07:00")
			}
			if _, err := s.db.Exec(`UPDATE sessions SET limited_until=? WHERE id=?`, c.t.Add(10*24*time.Hour).Unix(), target); err != nil {
				t.Fatal(err)
			}
			nudgeApply(t, s, chief, map[string]any{"kind": "idle_check", "agent": "target-agent", "until": expiry})
			if paused, err := s.nudgePaused(s.db, "idle_check", target); err != nil || !paused {
				t.Fatalf("active pause=%v %v", paused, err)
			}
			c.t = c.t.Add(2 * time.Hour)
			if until == "provider_reset" {
				if _, err := s.db.Exec(`UPDATE sessions SET limited_until=? WHERE id=?`, c.t.Add(-time.Second).Unix(), target); err != nil {
					t.Fatal(err)
				}
			}
			if until == "cap" {
				c.t = c.t.Add(7 * 24 * time.Hour)
			}
			out, err := nudgeCall(t, s, chief, "nudge.pauses.find", map[string]any{})
			if err != nil {
				t.Fatal(err)
			}
			if paused, err := s.nudgePaused(s.db, "idle_check", target); err != nil || paused {
				t.Fatalf("expired pause=%v %v", paused, err)
			}
			pauses := out["pauses"].([]any)
			if len(pauses) != 1 || pauses[0].(map[string]any)["ended_at"] == nil || pauses[0].(map[string]any)["ended_by"] == "" {
				t.Fatalf("expired=%v", out)
			}
		})
	}
}
func TestNudgePreviewReadOnly(t *testing.T) {
	s, c, chief, target := nudgeSetup(t)
	if _, err := s.db.Exec(`INSERT INTO profiles(session_id,task,updated_at) VALUES(?,'work',0) ON CONFLICT(session_id) DO UPDATE SET task='work'`, target); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, target, 21*time.Minute)
	if len(checks(t, s)) != 1 {
		t.Fatal("missing initial check")
	}
	var before int
	if err := s.db.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	out, err := nudgeCall(t, s, chief, "nudge.pause", map[string]any{"kind": "idle_check", "agent": "target-agent", "mode": "preview"})
	if err != nil {
		t.Fatal(err)
	}
	if out["messages_24h"] != float64(1) || !strings.Contains(stringMustJSON(out["sessions"]), "bob") {
		t.Fatalf("preview=%v", out)
	}
	var after int
	if err := s.db.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || after != before {
		t.Fatalf("preview wrote: %d -> %d (%v)", before, after, err)
	}
}
func stringMustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func TestNudgeChiefOnly(t *testing.T) {
	s, _, chief, target := nudgeSetup(t)
	id := nudgeApply(t, s, chief, map[string]any{"kind": "idle_check", "agent": "target-agent"})
	for frame, args := range map[string]map[string]any{"nudge.pause": {"kind": "idle_check", "agent": "target-agent", "mode": "apply"}, "nudge.resume": {"pause_id": id}, "nudge.pauses.find": {}} {
		if _, err := nudgeCall(t, s, target, frame, args); err == nil || !strings.Contains(err.Error(), "chief") {
			t.Fatalf("%s refused=%v", frame, err)
		}
	}
}
func TestNudgeDeskUndo(t *testing.T) {
	s, _, chief, _ := nudgeSetup(t)
	id := nudgeApply(t, s, chief, map[string]any{"kind": "idle_check", "agent": "target-agent", "reason": "provider busy"})
	log, err := s.DeskLog(1)
	if err != nil || len(log) != 1 || len(log[0].Options) != 2 {
		t.Fatalf("log=%v %v", log, err)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(log[0].Options[1]), &args); err != nil {
		t.Fatal(err)
	}
	node, _ := s.SessionNode(chief)
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Name: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	args["owner"] = owner
	raw, _ := json.Marshal(args)
	if _, err := (&Gateway{store: s}).handle(context.Background(), &nodeConn{id: node}, "nudge.undo", raw); err != nil {
		t.Fatal(err)
	}
	var ended string
	if err := s.db.QueryRow(`SELECT ended_by FROM nudge_pauses WHERE id=?`, id).Scan(&ended); err != nil || ended != "owner_undo" {
		t.Fatalf("undo=%q %v", ended, err)
	}
}
func TestNudgePauseRealMessagesUnaffected(t *testing.T) {
	s, _, chief, target := nudgeSetup(t)
	for _, kind := range []string{"idle_check", "autonomy_continue", "reply_reminder", "loop_tick", "step_reminder", "stuck_step", "undelivered", "waiting_notice", "wake_failed", "member_blocked"} {
		nudgeApply(t, s, chief, map[string]any{"kind": kind, "agent": "target-agent"})
	}
	if m, _, err := send(s, chief, target, "peer", ""); err != nil || m.To != target || m.State != protocol.StateStored {
		t.Fatalf("peer=%+v %v", m, err)
	}
	node, _ := s.SessionNode(chief)
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Name: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if m, _, err := send(s, owner, target, "owner", ""); err != nil || m.To != target || m.State != protocol.StateStored {
		t.Fatalf("owner=%+v %v", m, err)
	}

	targetNode, _ := s.SessionNode(target)
	pending, err := s.Pending(targetNode, false)
	if err != nil || len(pending) != 2 {
		t.Fatalf("real messages not deliverable: %+v %v", pending, err)
	}
}

// A reminder and a first notice can become ready in one pipeline batch.
// Pausing reminders must split that batch rather than suppress its first notice.
func TestNudgeStepMixedBatch(t *testing.T) {
	r := newDeliveryRig(t)
	callerNeedsSharedPipeline(t, r)
	if err := r.s.SetChief(protocol.ChiefSetRequest{SessionID: r.sonnet}, "owner"); err != nil {
		t.Fatal(err)
	}
	nudgeAgent(t, r.s, r.caller, "mixed-agent")
	c := mergedChange(t, r, "feature/mixed-pause", true)
	recentStepMessage(t, r, c.ID, protocol.StepRef{Step: protocol.StepRelease, AfterMerge: true})
	r.clk.t = r.clk.t.Add(3 * time.Hour)
	nudgeApply(t, r.s, r.sonnet, map[string]any{"kind": "step_reminder", "agent": "mixed-agent"})
	targets := []nodeTaskTarget{
		{id: c.ID, revision: c.Revision, ref: protocol.StepRef{Step: protocol.StepRelease, AfterMerge: true}, owner: callerTaskOwner, session: r.caller, isReminder: true},
		{id: c.ID, revision: c.Revision, ref: protocol.StepRef{Step: protocol.StepVerify, AfterMerge: true}, owner: callerTaskOwner, session: r.caller},
	}
	m, err := r.s.storeStepTaskMessages(targets)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID == "" || !strings.Contains(m.Body, "verify") || strings.Contains(m.Body, "ready since") {
		t.Fatalf("first notice lost in mixed batch: %+v", m)
	}
}

func TestNudgeUndoResumeRestoresPause(t *testing.T) {
	s, _, chief, target := nudgeSetup(t)
	id := nudgeApply(t, s, chief, map[string]any{"kind": "idle_check", "agent": "target-agent"})
	if _, err := nudgeCall(t, s, chief, "nudge.resume", map[string]any{"pause_id": id}); err != nil {
		t.Fatal(err)
	}
	log, err := s.DeskLog(1)
	if err != nil {
		t.Fatal(err)
	}
	var undo protocol.NudgeUndoArgs
	if err := json.Unmarshal([]byte(log[0].Options[1]), &undo); err != nil {
		t.Fatal(err)
	}
	if _, err := s.undoNudge(undo); err != nil {
		t.Fatal(err)
	}
	if paused, err := s.nudgePaused(s.db, "idle_check", target); err != nil || !paused {
		t.Fatalf("restore=%v %v", paused, err)
	}
	if _, err := s.undoNudge(undo); err == nil {
		t.Fatal("replayed consumed Undo")
	}
}

func TestNudgePauseDropsPreviouslyQueuedNudges(t *testing.T) {
	s, c, chief, target := nudgeSetup(t)
	m, err := s.storeCheck(protocol.AgentView{SessionID: target}, "day", 1, 0, c.t)
	if err != nil {
		t.Fatal(err)
	}
	nudgeApply(t, s, chief, map[string]any{"kind": "idle_check", "agent": "target-agent"})
	node, _ := s.SessionNode(target)
	pending, err := s.Pending(node, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, queued := range pending {
		if queued.ID == m.ID {
			t.Fatal("paused nudge is still queued for delivery")
		}
	}
}

func TestNudgeExpiredQueueSnapshotNotReplayed(t *testing.T) {
	s, c, chief, target := nudgeSetup(t)
	m, err := s.storeCheck(protocol.AgentView{SessionID: target}, "day", 1, 0, c.t)
	if err != nil {
		t.Fatal(err)
	}
	id := nudgeApply(t, s, chief, map[string]any{"kind": "idle_check", "agent": "target-agent"})
	if _, err := nudgeCall(t, s, chief, "nudge.resume", map[string]any{"pause_id": id}); err != nil {
		t.Fatal(err)
	}
	if allowed, err := s.allowNudgeDelivery(m); err != nil || allowed {
		t.Fatalf("stale snapshot allowed=%v %v", allowed, err)
	}
}

func TestNudgeStepExpiredReminderStillPaused(t *testing.T) {
	r, id := nudgeStepRig(t)
	r.g.dispatchStepTasks(context.Background())
	if _, err := nudgeCall(t, r.s, r.sonnet, "nudge.resume", map[string]any{"pause_id": id}); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(3 * time.Hour)
	r.g.dispatchStepTasks(context.Background())
	if len(taskBodies(t, r)) != 2 {
		t.Fatal("missing reminder before pause")
	}
	nudgeApply(t, r.s, r.sonnet, map[string]any{"kind": "step_reminder", "team": "Forge"})
	r.g.dispatchStepTasks(context.Background())
	if bodies := taskBodies(t, r); len(bodies) != 2 {
		t.Fatalf("expired reminder resent as first notice: %v", bodies)
	}
}
