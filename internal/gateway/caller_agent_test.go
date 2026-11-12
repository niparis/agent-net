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

// callerAgentRig is a delivery rig whose caller belongs to the agent "alpha".
// Machine a answers session wakes, and every woken session id lands in wakes.
type callerAgentRig struct {
	*deliveryRig
	second string
	wakes  chan string
}

// sessionOfAgent starts a session on machine a and gives it the agent's
// identity, as a real session has.
func (r *deliveryRig) sessionOfAgent(harness, native, agent string) string {
	r.t.Helper()
	id, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: harness, NativeID: native, Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err = r.s.db.Exec(`UPDATE sessions SET agent_id=? WHERE id=?`, agent, id); err != nil {
		r.t.Fatal(err)
	}
	return id
}

// moveToAgent puts a session under another agent. A rig puts every session of
// the repo "demo" under one agent, and sessions of one agent share the
// caller's steps.
func (r *deliveryRig) moveToAgent(session, agent string) {
	r.t.Helper()
	if err := r.s.AddArchitect(agent, "owner"); err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET agent_id=? WHERE id=?`, agent, session); err != nil {
		r.t.Fatal(err)
	}
}

func newCallerAgentRig(t *testing.T) *callerAgentRig {
	t.Helper()
	r := &callerAgentRig{deliveryRig: newDeliveryRig(t), wakes: make(chan string, 8)}
	r.g.logger = log.New(io.Discard, "", 0)
	r.g.conns = map[string]*nodeConn{}
	fakeOwnerNode(t, r.g, r.a, func(h protocol.Header, raw []byte) error {
		if h.Type == protocol.FrameSessionWake {
			var p struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			r.wakes <- p.SessionID
		}
		return nil
	})
	if err := r.s.AddArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET agent_id='alpha' WHERE id=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	r.second = r.sessionOfAgent("claude", "second", "alpha")
	return r
}

// submitAs submits a change whose caller is the given session.
func (r *callerAgentRig) submitAs(caller, branch, head string) string {
	q := r.workerSubmit(branch, head)
	q.SessionID = caller
	return r.submit(r.a, q).Change.Change.ID
}

func (r *callerAgentRig) accept(session, change, commit string) (protocol.StepReportResult, error) {
	return r.report(r.a, protocol.StepReportRequest{SessionID: session, ChangeID: change, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commit})
}

func (r *callerAgentRig) stepMessageCount(session string) int {
	r.t.Helper()
	var n int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_step_messages WHERE session_id=?`, session).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *callerAgentRig) waitWake() string {
	r.t.Helper()
	select {
	case id := <-r.wakes:
		return id
	case <-time.After(2 * time.Second):
		r.t.Fatal("no session wake")
		return ""
	}
}

func TestCallerStepOfferedToLiveSessionOfCallerAgent(t *testing.T) {
	r := newCallerAgentRig(t)
	if err := r.s.SetSessionOffline(r.a, r.caller); err != nil {
		t.Fatal(err)
	}
	id := r.submitAs(r.caller, "feature/offered", commitA)
	r.g.dispatchStepTasks(context.Background())
	var body string
	if err := r.s.db.QueryRow(`SELECT m.body FROM delivery_step_messages d JOIN messages m ON m.id=d.message_id WHERE d.session_id=?`, r.second).Scan(&body); err != nil {
		t.Fatalf("live session of the caller's agent got no task: %v", err)
	}
	if !strings.Contains(body, id) {
		t.Fatalf("task for the second session does not name %s: %s", id, body)
	}
	if n := r.stepMessageCount(r.caller); n != 0 {
		t.Fatalf("offline caller session got %d task messages", n)
	}
	var wakes int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM retired_session_wakes WHERE session_id=?`, r.caller).Scan(&wakes); err != nil || wakes != 0 {
		t.Fatalf("offline caller was woken although its agent has a live session: %d, %v", wakes, err)
	}
}

func TestSecondSessionOfCallerAgentReportsCallerStep(t *testing.T) {
	r := newCallerAgentRig(t)
	id := r.submitAs(r.caller, "feature/second", commitA)
	res, err := r.accept(r.second, id, commitA)
	if err != nil {
		t.Fatalf("second session of the caller's agent refused: %v", err)
	}
	if res.Evidence.By != r.second {
		t.Fatalf("accept recorded by %q, want %q", res.Evidence.By, r.second)
	}
}

func TestOtherAgentCannotReportOrClaimCallerStep(t *testing.T) {
	r := newCallerAgentRig(t)
	id := r.submitAs(r.caller, "feature/other", commitA)
	if err := r.s.AddArchitect("beta", "owner"); err != nil {
		t.Fatal(err)
	}
	other := r.sessionOfAgent("claude", "other", "beta")
	if _, err := r.accept(other, id, commitA); err == nil || !strings.Contains(err.Error(), "owned by the caller") {
		t.Fatalf("report by another agent = %v, want owned by the caller", err)
	}
	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: other, ChangeID: id, StepRef: stepAccept, Commit: commitA}); err == nil {
		t.Fatal("another agent claimed the caller's step")
	}
}

// offlineCodexCaller is the only session of agent "alpha" besides its live
// claude sessions, which the caller tests put offline.
func (r *callerAgentRig) onlyOfflineSession(harness string) string {
	r.t.Helper()
	id := r.sessionOfAgent(harness, "only-"+harness, "alpha")
	for _, s := range []string{r.caller, r.second} {
		if err := r.s.SetSessionOffline(r.a, s); err != nil {
			r.t.Fatal(err)
		}
	}
	if err := r.s.SetSessionOffline(r.a, id); err != nil {
		r.t.Fatal(err)
	}
	return id
}

func TestOfflineCallerAgentWokenOnceWithItsWaitingSteps(t *testing.T) {
	r := newCallerAgentRig(t)
	caller := r.onlyOfflineSession("codex")
	first := r.submitAs(caller, "feature/wake-1", commitA)
	second := r.submitAs(caller, "feature/wake-2", commitB)
	r.g.dispatchStepTasks(context.Background())
	var n int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE recipient=? AND idem_key LIKE 'delivery:agentwake:%'`, caller).Scan(&n); err != nil || n != 0 {
		t.Fatalf("agent was woken before the stuck limit: %d, %v", n, err)
	}
	r.clk.t = r.clk.t.Add(13 * time.Hour)
	r.g.dispatchStepTasks(context.Background())
	if id := r.waitWake(); id != caller {
		t.Fatalf("woke %s, want %s", id, caller)
	}
	var body string
	if err := r.s.db.QueryRow(`SELECT body FROM messages WHERE recipient=? AND idem_key LIKE 'delivery:agentwake:%'`, caller).Scan(&body); err != nil {
		t.Fatalf("no wake message with the list: %v", err)
	}
	for _, id := range []string{first, second} {
		if !strings.Contains(body, id) {
			t.Fatalf("wake message misses %s: %s", id, body)
		}
	}
	r.g.dispatchStepTasks(context.Background())
	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE idem_key LIKE 'delivery:agentwake:%'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("wake messages after a second pass = %d, want 1 (%v)", n, err)
	}
}

// leadOf makes agent "lead" the lead of demo and starts one live session of it.
func (r *callerAgentRig) leadOf() string {
	r.t.Helper()
	leadDemoWith(r.t, r.deliveryRig, "lead")
	return agentSessionOn(r.t, r.deliveryRig, r.a, "lead")
}

func TestNonWakeableCallerAgentStepsGoToRepoLead(t *testing.T) {
	r := newCallerAgentRig(t)
	caller := r.onlyOfflineSession("pi")
	// The machine refused to resume the agent's other sessions.
	for _, id := range []string{r.caller, r.second} {
		if _, err := r.s.db.Exec(`INSERT INTO session_wake_failures(session_id,failed_at,reason) VALUES(?,?,'test')`, id, r.clk.t.UnixNano()); err != nil {
			t.Fatal(err)
		}
	}
	lead := r.leadOf()
	id := r.submitAs(caller, "feature/lead", commitA)
	if _, err := r.accept(lead, id, commitA); err == nil {
		t.Fatal("the repo lead acted on a caller step before the offer")
	}
	r.clk.t = r.clk.t.Add(13 * time.Hour)
	r.g.dispatchStepTasks(context.Background())
	var body string
	if err := r.s.db.QueryRow(`SELECT m.body FROM delivery_step_messages d JOIN messages m ON m.id=d.message_id WHERE d.session_id=?`, lead).Scan(&body); err != nil || !strings.Contains(body, id) {
		t.Fatalf("lead was not offered %s: %q, %v", id, body, err)
	}
	if res, err := r.accept(lead, id, commitA); err != nil || res.Evidence.By != lead {
		t.Fatalf("lead accept = %+v, %v", res, err)
	}
}

func TestUnansweredWakeFallsBackToRepoLead(t *testing.T) {
	r := newCallerAgentRig(t)
	caller := r.onlyOfflineSession("codex")
	lead := r.leadOf()
	id := r.submitAs(caller, "feature/unanswered", commitA)
	r.clk.t = r.clk.t.Add(13 * time.Hour)
	r.g.dispatchStepTasks(context.Background())
	if got := r.waitWake(); got != caller {
		t.Fatalf("woke %s, want %s", got, caller)
	}
	if _, err := r.accept(lead, id, commitA); err == nil {
		t.Fatal("lead was offered the step while the wake was still under way")
	}
	// The wake window passes and nobody of the agent is online.
	r.clk.t = r.clk.t.Add(wakeTimeout + time.Minute)
	r.g.dispatchStepTasks(context.Background())
	if _, err := r.accept(lead, id, commitA); err != nil {
		t.Fatalf("lead refused after the wake window: %v", err)
	}
}

func (r *callerAgentRig) deskLines() []string {
	r.t.Helper()
	rows, err := r.s.db.Query(`SELECT text FROM inbox_items WHERE source_ref LIKE 'waiting-work:%' AND status='open' ORDER BY source_ref`)
	if err != nil {
		r.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			r.t.Fatal(err)
		}
		out = append(out, text)
	}
	return out
}

func TestWaitingWorkDeskLineCountsUpdatesAndCloses(t *testing.T) {
	r := newCallerAgentRig(t)
	caller := r.onlyOfflineSession("pi")
	leadDemoWith(t, r.deliveryRig, "lead")
	commits := []string{commitA, commitB, commitC, strings.Repeat("d", 40)}
	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, r.submitAs(caller, fmt.Sprintf("feature/desk-%d", i), commits[i]))
	}
	r.clk.t = r.clk.t.Add(23 * time.Hour)
	r.g.dispatchStepTasks(context.Background())
	if lines := r.deskLines(); len(lines) != 0 {
		t.Fatalf("a line before 24 hours: %v", lines)
	}
	r.clk.t = r.clk.t.Add(2 * time.Hour)
	r.g.dispatchStepTasks(context.Background())
	want := "demo: 3 finished changes waiting for accept, oldest 25 hours, lead offline"
	if lines := r.deskLines(); len(lines) != 1 || lines[0] != want {
		t.Fatalf("desk lines = %q, want [%q]", lines, want)
	}
	var item string
	if err := r.s.db.QueryRow(`SELECT id FROM inbox_items WHERE source_ref='waiting-work:demo' AND status='open'`).Scan(&item); err != nil {
		t.Fatal(err)
	}
	// A fourth change updates the same line.
	ids = append(ids, r.submitAs(caller, "feature/desk-3", commits[3]))
	r.clk.t = r.clk.t.Add(24 * time.Hour)
	r.g.dispatchStepTasks(context.Background())
	want = "demo: 4 finished changes waiting for accept, oldest 2 days, lead offline"
	var again, text string
	if err := r.s.db.QueryRow(`SELECT id,text FROM inbox_items WHERE source_ref='waiting-work:demo' AND status='open'`).Scan(&again, &text); err != nil {
		t.Fatal(err)
	}
	if again != item || text != want {
		t.Fatalf("line not updated in place: id %s -> %s, %q, want %q", item, again, text, want)
	}
	// The agent comes back and accepts everything: the line closes.
	if _, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "pi", NativeID: "only-pi", Repo: protocol.RepoInfo{Project: "demo"}}); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		if _, err := r.accept(caller, id, commits[i]); err != nil {
			t.Fatal(err)
		}
	}
	r.g.dispatchStepTasks(context.Background())
	if lines := r.deskLines(); len(lines) != 0 {
		t.Fatalf("desk line stayed open at zero: %v", lines)
	}
}

// callerTaskOwner of a step, or "" when the step has no task.
func (r *callerAgentRig) taskOwner(change string, ref protocol.StepRef) string {
	r.t.Helper()
	var owner string
	err := r.s.db.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND step=? AND for_step=? AND after_merge=? AND completed=0`,
		change, ref.Step, ref.For, boolInt(ref.AfterMerge)).Scan(&owner)
	if err != nil && err.Error() != "sql: no rows in result set" {
		r.t.Fatal(err)
	}
	return owner
}

// Every step of this pipeline is owned by the caller, so none is shared. The
// caller still gets a task for accept, and later for release and verify.
func TestCallerOnlyPipelineQueuesStepTasks(t *testing.T) {
	r := newCallerAgentRig(t)
	id := r.submitAs(r.caller, "feature/caller-only", commitA)
	if got := r.taskOwner(id, stepAccept); got != callerTaskOwner {
		t.Fatalf("accept task owner = %q, want %q", got, callerTaskOwner)
	}
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepTest}, Result: protocol.StepResultPass, Commit: commitA,
		Fields: map[string]string{protocol.FieldSuite: protocol.SuiteFull}})
	r.mustReport(r.a, mergeReport(protocol.Change{ID: id}, r.caller))
	for _, ref := range []protocol.StepRef{ciTestRef, releaseRef, verifyRef} {
		if got := r.taskOwner(id, ref); got != callerTaskOwner {
			t.Fatalf("%s task owner = %q, want %q", ref, got, callerTaskOwner)
		}
		fields := map[string]string{}
		if ref == ciTestRef {
			fields[protocol.FieldSuite] = protocol.SuiteFull
		}
		r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: ref, Result: protocol.StepResultPass, Commit: commitMerged, Fields: fields})
	}
}

// The project's merger is a session of the same agent as the caller. It owns
// the merge step, so it is never handed the caller's steps and may not act on
// them. With the caller offline the steps follow the wake and the lead.
func TestMergerSessionOfCallerAgentNeverGetsCallerSteps(t *testing.T) {
	r := newCallerAgentRig(t)
	merger := r.second
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: merger}, "owner"); err != nil {
		t.Fatal(err)
	}
	lead := r.leadOf()
	if err := r.s.SetSessionOffline(r.a, r.caller); err != nil {
		t.Fatal(err)
	}
	id := r.submitAs(r.caller, "feature/merger-agent", commitA)
	r.g.dispatchStepTasks(context.Background())
	if n := r.stepMessageCount(merger); n != 0 {
		t.Fatalf("the merger got %d caller step messages", n)
	}
	if _, err := r.accept(merger, id, commitA); err == nil || !strings.Contains(err.Error(), "owned by the caller") {
		t.Fatalf("merger report on accept = %v, want owned by the caller", err)
	}
	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: merger, ChangeID: id, StepRef: stepAccept, Commit: commitA}); err == nil {
		t.Fatal("the merger claimed the caller's accept")
	}
	// The agent has an online session (the merger), so nobody is woken: the
	// task goes to the lead once it waited past the stuck limit.
	r.clk.t = r.clk.t.Add(13 * time.Hour)
	r.g.dispatchStepTasks(context.Background())
	var wakes int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE idem_key LIKE 'delivery:agentwake:%'`).Scan(&wakes); err != nil || wakes != 0 {
		t.Fatalf("woke the agent while its merger session is online: %d, %v", wakes, err)
	}
	r.g.dispatchStepTasks(context.Background())
	if n := r.stepMessageCount(merger); n != 0 {
		t.Fatalf("the merger got %d caller step messages after the offer", n)
	}
	if _, err := r.accept(lead, id, commitA); err != nil {
		t.Fatalf("lead refused after the wake window: %v", err)
	}
}

// A caller that does its own steps reports each at once, so a project whose
// steps all belong to the caller sends no notice for a young step. The notice
// comes once the step has waited one reminder interval.
func TestCallerOnlyStepNoticeWaitsForTheReminderInterval(t *testing.T) {
	r := newCallerAgentRig(t)
	r.submitAs(r.caller, "feature/quiet", commitA)
	r.g.dispatchStepTasks(context.Background())
	if n := r.stepMessageCount(r.caller); n != 0 {
		t.Fatalf("a young caller step sent %d messages", n)
	}
	r.clk.t = r.clk.t.Add(2*time.Hour + time.Minute)
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTaskBatches()
	var n int
	if err := r.s.db.QueryRow(`SELECT count(DISTINCT message_id) FROM delivery_step_messages WHERE session_id=?`, r.caller).Scan(&n); err != nil || n != 1 {
		t.Fatalf("a caller step that waited past the reminder interval sent %d messages, want 1 (%v)", n, err)
	}
}

// A session of the caller's agent that is online but owns another step of the
// change (here it is the project's merger) may not act on the caller's steps,
// yet the agent is not offline. The "no session of yours was online" wake must
// not be sent to it.
func TestCallerWakeNotSentWhileASessionOfTheAgentIsOnline(t *testing.T) {
	r := newCallerAgentRig(t)
	if err := r.s.SetSessionOffline(r.a, r.caller); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`INSERT INTO project_mergers(project,node_id,session_id) VALUES('demo',?,?)`, r.a, r.second); err != nil {
		t.Fatal(err)
	}
	r.submitAs(r.caller, "feature/online-merger", commitA)
	r.clk.t = r.clk.t.Add(13 * time.Hour)
	r.g.dispatchStepTasks(context.Background())
	var n int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE idem_key LIKE 'delivery:agentwake:%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("woke the agent while its session %s is online: %d wake messages, %v", r.second, n, err)
	}
}
