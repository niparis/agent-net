package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// When a restart cannot keep the session ID (a new conversation), the old
// session's work moves to the new one in one transaction: the caller role of
// its running and queued workers, its open change and claimed step, and an
// undelivered request. The old record is marked replaced, not renamed.
func TestRestartMovesTheWorkWhenANewConversationGetsANewID(t *testing.T) {
	r := newDeliveryRig(t)
	// A shared pipeline gives the caller real step tasks it can claim.
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: "reviews"}, "owner"); err != nil {
		t.Fatal(err)
	}
	// The caller quits. A new conversation registers and takes the name.
	id := r.submit(r.a, r.workerSubmit("feature/restart", commitA)).Change.Change.ID
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept,
		Result: protocol.StepResultPass, Commit: commitA}); err != nil {
		t.Fatal(err)
	}
	testStep := protocol.StepRef{Step: protocol.StepTest}
	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: r.caller, ChangeID: id, StepRef: testStep, Commit: commitA}); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetSessionOffline(r.a, r.caller); err != nil {
		t.Fatal(err)
	}
	fresh, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "caller-copy",
		Repo: protocol.RepoInfo{Root: "/code/demo", Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	// A request the old session has not been given yet.
	pending, _, err := r.s.StoreMessage(protocol.MsgSend{Sender: r.worker, Recipient: r.caller, Type: protocol.TypeRequest, Body: "the hand-back"})
	if err != nil {
		t.Fatal(err)
	}
	// A worker start that is still queued at the gateway.
	if err := r.s.TrackWorkerStart("ref-queued", r.a, r.caller, "demo", "claude", ""); err != nil {
		t.Fatal(err)
	}

	moved, err := r.s.ReplaceSession(r.a, r.caller, fresh)
	if err != nil || !moved {
		t.Fatalf("replace: moved=%v err=%v", moved, err)
	}

	// The running worker's caller.
	w, ok, err := r.s.Agent(r.worker)
	if err != nil || !ok || w.Worker == nil || w.Worker.Owner != fresh {
		t.Fatalf("the running worker still names the old caller: %+v %v", w, err)
	}
	// The queued worker start.
	var queued string
	if err := r.s.db.QueryRow(`SELECT owner FROM worker_starts WHERE ref='ref-queued'`).Scan(&queued); err != nil || queued != fresh {
		t.Fatalf("the queued worker still names the old caller: %q %v", queued, err)
	}
	// The open change and its claimed step: only the new session may report it.
	ch, err := r.s.GetChange(id)
	if err != nil || ch.Change.Caller != fresh {
		t.Fatalf("the open change still names the old caller: %+v %v", ch.Change, err)
	}
	var claimed string
	if err := r.s.db.QueryRow(`SELECT claimed_by FROM delivery_step_tasks WHERE change_id=? AND step='test'`, id).Scan(&claimed); err != nil || claimed != fresh {
		t.Fatalf("the claimed step still names the old caller: %q %v", claimed, err)
	}
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: fresh, ChangeID: id, StepRef: testStep,
		Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldSuite: protocol.SuiteChanged}}); err != nil {
		t.Fatalf("the new session could not report the claimed step: %v", err)
	}
	// The undelivered request.
	msg, ok, err := r.s.Message(pending.ID)
	if err != nil || !ok || msg.To != fresh {
		t.Fatalf("the undelivered request still names the old session: %+v %v", msg, err)
	}

	// The old record is replaced, its name is cleared, and it says which session
	// took the work. Only the new session carries "demo".
	before, ok, err := r.s.Agent(r.caller)
	if err != nil || !ok {
		t.Fatalf("read the replaced session: %+v %v", before, err)
	}
	if before.ReplacedBy != fresh || before.Profile.Name != "" {
		t.Fatalf("the old record is not marked replaced with cleared name: %+v", before)
	}
	after, ok, err := r.s.Agent(fresh)
	if err != nil || !ok || after.Profile.Name != "demo" {
		t.Fatalf("the new session did not keep the name: %+v %v", after, err)
	}
	// A later discovery report of the exited conversation must not rename or
	// revive the replaced record.
	if _, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "caller", Status: "exited",
		Repo: protocol.RepoInfo{Root: "/code/demo", Project: "demo"}}); err != nil {
		t.Fatal(err)
	}
	again, ok, err := r.s.Agent(r.caller)
	if err != nil || !ok || again.Profile.Name != "" || again.Online || again.ReplacedBy != fresh {
		t.Fatalf("the replaced record changed on a later report: %+v %v", again, err)
	}
}

// The owner's repair for a restart that already orphaned a session: the
// session_replace action moves the old session's work to the one that replaced
// it. The owner's command line may be on another machine, so the move uses the
// old session's own node.
func TestOwnerReplaceSessionMovesTheWork(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: "reviews"}, "owner"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/repair", commitA)).Change.Change.ID
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept,
		Result: protocol.StepResultPass, Commit: commitA}); err != nil {
		t.Fatal(err)
	}
	testStep := protocol.StepRef{Step: protocol.StepTest}
	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: r.caller, ChangeID: id, StepRef: testStep, Commit: commitA}); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetSessionOffline(r.a, r.caller); err != nil {
		t.Fatal(err)
	}
	fresh, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "caller-repair",
		Repo: protocol.RepoInfo{Root: "/code/demo", Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	pending, _, err := r.s.StoreMessage(protocol.MsgSend{Sender: r.worker, Recipient: r.caller, Type: protocol.TypeRequest, Body: "the hand-back"})
	if err != nil {
		t.Fatal(err)
	}

	// The owner's command line is on the other machine.
	res, err := deliveryCall[protocol.SessionReplaceResult](r, r.b, protocol.FrameSessionOwnerReplace,
		protocol.SessionOwnerReplace{OldSession: r.caller, NewSession: fresh, Actor: "owner"})
	if err != nil || !res.Moved {
		t.Fatalf("owner replace: %+v %v", res, err)
	}

	// The running worker's caller.
	w, ok, err := r.s.Agent(r.worker)
	if err != nil || !ok || w.Worker == nil || w.Worker.Owner != fresh {
		t.Fatalf("the running worker still names the old caller: %+v %v", w, err)
	}
	// The open change and its claimed step: only the new session may report it.
	ch, err := r.s.GetChange(id)
	if err != nil || ch.Change.Caller != fresh {
		t.Fatalf("the open change still names the old caller: %+v %v", ch.Change, err)
	}
	var claimed string
	if err := r.s.db.QueryRow(`SELECT claimed_by FROM delivery_step_tasks WHERE change_id=? AND step='test'`, id).Scan(&claimed); err != nil || claimed != fresh {
		t.Fatalf("the claimed step still names the old caller: %q %v", claimed, err)
	}
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: fresh, ChangeID: id, StepRef: testStep,
		Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldSuite: protocol.SuiteChanged}}); err != nil {
		t.Fatalf("the new session could not report the claimed step: %v", err)
	}
	// The undelivered request.
	msg, ok, err := r.s.Message(pending.ID)
	if err != nil || !ok || msg.To != fresh {
		t.Fatalf("the undelivered request still names the old session: %+v %v", msg, err)
	}
	// The old record is replaced and says which session took the work.
	before, ok, err := r.s.Agent(r.caller)
	if err != nil || !ok || before.ReplacedBy != fresh {
		t.Fatalf("the old record is not marked replaced by the new one: %+v %v", before, err)
	}
}

// The owner may only move a session's work to a session on the same machine.
func TestOwnerReplaceSessionRefusesAnotherMachine(t *testing.T) {
	r := newDeliveryRig(t)
	fresh, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "claude", NativeID: "elsewhere",
		Repo: protocol.RepoInfo{Root: "/code/demo", Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = deliveryCall[protocol.SessionReplaceResult](r, r.a, protocol.FrameSessionOwnerReplace,
		protocol.SessionOwnerReplace{OldSession: r.caller, NewSession: fresh, Actor: "owner"})
	if err == nil || !strings.Contains(err.Error(), "another machine") {
		t.Fatalf("a move across machines was not refused: %v", err)
	}
	before, ok, err := r.s.Agent(r.caller)
	if err != nil || !ok || before.ReplacedBy != "" {
		t.Fatalf("the refused move changed the old record: %+v %v", before, err)
	}
	w, ok, err := r.s.Agent(r.worker)
	if err != nil || !ok || w.Worker == nil || w.Worker.Owner != r.caller {
		t.Fatalf("the refused move changed the worker: %+v %v", w, err)
	}
}

// duplicateStepNotices sets up the collision decision 0148 allows: the
// caller gets the notices of its change, goes offline, and the next dispatch
// sends the same steps to another live session of its agent. It returns the
// change, that other session, and the accept notice each session holds.
func duplicateStepNotices(t *testing.T, r *deliveryRig) (change, fresh, oldNotice, newNotice string) {
	t.Helper()
	callerNeedsSharedPipeline(t, r)
	change = r.submit(r.a, r.workerSubmit("feature/duplicate", commitA)).Change.Change.ID
	notice := func(session string) string {
		t.Helper()
		var id string
		if err := r.s.db.QueryRow(`SELECT message_id FROM `+stepNoticesSQL+` WHERE change_id=? AND step='accept' AND session_id=?`,
			change, session).Scan(&id); err != nil {
			t.Fatalf("session %s has no accept notice: %v", session, err)
		}
		return id
	}
	r.g.dispatchStepTasks(context.Background())
	oldNotice = notice(r.caller)
	// The rig's other live session of the agent is on the other machine; a
	// replace must stay on one machine, so only the sonnet session stays live.
	if err := r.s.SetSessionOffline(r.b, r.deepseek); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetSessionOffline(r.a, r.caller); err != nil {
		t.Fatal(err)
	}
	r.g.dispatchStepTasks(context.Background())
	return change, r.sonnet, oldNotice, notice(r.sonnet)
}

// Two sessions of one agent can hold a notice for the same step. Replacing one
// with the other keeps the new session's notice and drops the old duplicate
// instead of failing on the step message key. The old notice is expired, so it
// neither wakes the old session nor reaches the new one twice.
func TestReplaceSessionMergesDuplicateStepNotices(t *testing.T) {
	r := newDeliveryRig(t)
	change, fresh, oldNotice, newNotice := duplicateStepNotices(t, r)

	moved, err := r.s.ReplaceSession(r.a, r.caller, fresh)
	if err != nil || !moved {
		t.Fatalf("replace: moved=%v err=%v", moved, err)
	}
	// Each step keeps one notice, the new session's.
	rows, err := r.s.db.Query(`SELECT step,session_id,message_id FROM `+stepNoticesSQL+` WHERE change_id=? ORDER BY step`, change)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var step, session, message string
		if err := rows.Scan(&step, &session, &message); err != nil {
			t.Fatal(err)
		}
		if session != fresh || step == "accept" && message != newNotice {
			got = append(got, "wrong:")
		}
		got = append(got, step)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "accept test" {
		t.Fatalf("step notices after replace: %v, want one accept and one test notice of %s", got, fresh)
	}
	var state, to string
	if err := r.s.db.QueryRow(`SELECT state,recipient FROM messages WHERE id=?`, oldNotice).Scan(&state, &to); err != nil {
		t.Fatal(err)
	}
	if state != "expired" {
		t.Fatalf("the old duplicate notice is %s for %s, want expired", state, to)
	}
	// The old session's worker now reports to the new session, and nothing
	// that can still be delivered waits for the old one, so nothing wakes it.
	w, ok, err := r.s.Agent(r.worker)
	if err != nil || !ok || w.Worker == nil || w.Worker.Owner != fresh {
		t.Fatalf("the worker still names the old caller: %+v %v", w, err)
	}
	var waiting int
	if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE recipient=? AND state IN ('held','stored','dispatched','failed')`, r.caller).Scan(&waiting); err != nil || waiting != 0 {
		t.Fatalf("%d messages still wait for the replaced session: %v", waiting, err)
	}
}

// The move is one transaction: a failure at its last write leaves every
// earlier move undone.
func TestReplaceSessionFailureMovesNothing(t *testing.T) {
	r := newDeliveryRig(t)
	change, fresh, oldNotice, _ := duplicateStepNotices(t, r)
	if _, err := r.s.db.Exec(`CREATE TRIGGER fail_replace BEFORE UPDATE OF replaced_by ON sessions
		BEGIN SELECT RAISE(ABORT, 'replace failed'); END`); err != nil {
		t.Fatal(err)
	}

	if moved, err := r.s.ReplaceSession(r.a, r.caller, fresh); err == nil || moved {
		t.Fatalf("replace with a failing write: moved=%v err=%v", moved, err)
	}
	w, ok, err := r.s.Agent(r.worker)
	if err != nil || !ok || w.Worker == nil || w.Worker.Owner != r.caller {
		t.Fatalf("a failed replace moved the worker: %+v %v", w, err)
	}
	if ch, err := r.s.GetChange(change); err != nil || ch.Change.Caller != r.caller {
		t.Fatalf("a failed replace moved the change: %+v %v", ch.Change, err)
	}
	var session, state string
	if err := r.s.db.QueryRow(`SELECT d.session_id,m.state FROM `+stepNoticesSQL+` d JOIN messages m ON m.id=d.message_id
		WHERE d.message_id=?`, oldNotice).Scan(&session, &state); err != nil || session != r.caller || state == "expired" {
		t.Fatalf("a failed replace changed the old notice: %q %q %v", session, state, err)
	}
}

// Replacing an online lead session (for example a Pi session that is still
// running) moves the name, repo_lead and team_lead to the new session and keeps
// the old session offline, unnamed and unroutable even if it reports again.
func TestReplaceOnlineLeadSessionMovesNameAndLeadRole(t *testing.T) {
	s := teamStore(t)
	var old, node, harness, native string
	if err := s.db.QueryRow(`SELECT id,node_id,harness,native_id FROM sessions WHERE repo_id='demo'`).Scan(&old, &node, &harness, &native); err != nil {
		t.Fatal(err)
	}
	var outsider string
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='other'`).Scan(&outsider); err != nil {
		t.Fatal(err)
	}
	owner := protocol.GraphRef{Kind: "agent", ID: "demo"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Purpose: "Shared work", Owner: &owner})
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "alpha", AsksTo: protocol.AsksToLead})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})
	if err := s.AddArchitect("helper", "owner"); err != nil {
		t.Fatal(err)
	}
	member, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "helper", Repo: protocol.RepoInfo{Root: "/code/demo", Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET agent_id='helper' WHERE id=?`, member); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenameSession(old, "homelab-agent - lead"); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "fresh", Status: "idle",
		Repo: protocol.RepoInfo{Root: "/code/demo", Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.ReplaceSessionForOwner(old, fresh, "cto")
	if err != nil || !res.Moved {
		t.Fatalf("replace: %+v %v", res, err)
	}
	// Simulate a still-running old session reporting again on the next sync.
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: harness, NativeID: native, Status: "idle",
		Repo: protocol.RepoInfo{Root: "/code/demo", Project: "demo"}}); err != nil {
		t.Fatal(err)
	}

	oldView, ok, err := s.Agent(old)
	if err != nil || !ok {
		t.Fatalf("read old session: %+v %v", oldView, err)
	}
	if oldView.Online {
		t.Fatalf("old session stayed online after replace: %+v", oldView)
	}
	if oldView.Profile.Name == "homelab-agent - lead" {
		t.Fatalf("old session still carries the name %q after replace", oldView.Profile.Name)
	}
	if oldView.RepoLead || oldView.TeamLead {
		t.Fatalf("old session still holds lead flags: repo_lead=%v team_lead=%v", oldView.RepoLead, oldView.TeamLead)
	}
	freshView, ok, err := s.Agent(fresh)
	if err != nil || !ok {
		t.Fatalf("read new session: %+v %v", freshView, err)
	}
	if !freshView.Online || freshView.Profile.Name != "homelab-agent - lead" {
		t.Fatalf("new session did not take online name: online=%v name=%q", freshView.Online, freshView.Profile.Name)
	}
	if !freshView.RepoLead || !freshView.TeamLead {
		t.Fatalf("new session did not receive lead flags: repo_lead=%v team_lead=%v", freshView.RepoLead, freshView.TeamLead)
	}
	for _, addr := range []string{"homelab-agent - lead", "repo:demo", "team:Alpha"} {
		got, err := s.ResolveLeadAddress(addr)
		if err != nil || got != fresh {
			t.Fatalf("ResolveLeadAddress(%q) = %q (%v), want %q", addr, got, err, fresh)
		}
	}
	tl, err := s.TeamLeadFor(member)
	if err != nil || tl.LeadSession != fresh {
		t.Fatalf("TeamLeadFor(%s) = %+v (%v), want %q", member, tl, err, fresh)
	}
	msg, _, _, err := s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: member, Body: "question"})
	if err != nil || msg.To != fresh {
		t.Fatalf("StoreLeadMessage routed to %q (%v), want %q", msg.To, err, fresh)
	}
}

// Replacing a live busy lead session through the owner/CTO session_change
// replace path records replaced_by, moves the lead role, member reporting and
// workers to the new session, and returns moved: true.
func TestOwnerReplaceBusyLeadSessionMovesMembersAndWorkers(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("homelab", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddProjects(node, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "homelab-agent", Path: "/srv/homelab"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddArchitect("homelab-lead", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE repos SET lead_agent_id='homelab-lead' WHERE id='homelab-agent'`); err != nil {
		t.Fatal(err)
	}
	oldLead, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "old-lead", Status: "busy",
		Repo: protocol.RepoInfo{Root: "/srv/homelab", Project: "homelab-agent"}})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "w-1", Status: "busy",
		Repo:   protocol.RepoInfo{Root: "/srv/homelab", Project: "homelab-agent"},
		Worker: &protocol.WorkerInfo{ID: "w_homelab", Owner: oldLead, Project: "homelab-agent"}})
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "member-1", Status: "idle",
		Repo: protocol.RepoInfo{Root: "/srv/homelab", Project: "homelab-agent"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET agent_id='homelab-agent' WHERE id=?`, member); err != nil {
		t.Fatal(err)
	}
	newLead, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "new-lead", Status: "idle",
		Repo: protocol.RepoInfo{Root: "/srv/homelab", Project: "homelab-agent"}})
	if err != nil {
		t.Fatal(err)
	}

	res, err := s.ReplaceSessionForOwner(oldLead, newLead, "cto")
	if err != nil || !res.Moved {
		t.Fatalf("ReplaceSessionForOwner(busy lead): res=%+v err=%v, want moved=true", res, err)
	}
	oldView, ok, err := s.Agent(oldLead)
	if err != nil || !ok || oldView.ReplacedBy != newLead || oldView.Online {
		t.Fatalf("old lead after replace: %+v %v", oldView, err)
	}
	newView, ok, err := s.Agent(newLead)
	if err != nil || !ok || newView.AgentID != "homelab-lead" || !newView.RepoLead || !newView.RepoLeadExplicit {
		t.Fatalf("new lead after replace: %+v %v", newView, err)
	}
	if got := agentLeadSession(t, s, "homelab-lead"); got != newLead {
		t.Fatalf("agents.lead_session = %q, want %q", got, newLead)
	}
	wView, ok, err := s.Agent(worker)
	if err != nil || !ok || wView.Worker == nil || wView.Worker.Owner != newLead {
		t.Fatalf("worker owner after replace: %+v %v", wView, err)
	}
	mView, ok, err := s.Agent(member)
	if err != nil || !ok || mView.ReportsTo != "homelab-lead" {
		t.Fatalf("member after replace: %+v %v", mView, err)
	}
}
