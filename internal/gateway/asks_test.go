package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// askRow reads an ask straight from the store, open or closed.
func askRow(t *testing.T, s *Store, where string, args ...any) (status, answer, kind string) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT status,answer,kind FROM inbox_items WHERE `+where, args...).Scan(&status, &answer, &kind); err != nil {
		t.Fatalf("ask %s: %v", where, err)
	}
	return
}

func askByKind(asks []protocol.Ask, kind string) []protocol.Ask {
	var out []protocol.Ask
	for _, a := range asks {
		if a.Kind == kind {
			out = append(out, a)
		}
	}
	return out
}

func heldRig(t *testing.T) (*Store, *clock, string, string) {
	t.Helper()
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project=CASE id WHEN ? THEN 'consumer' ELSE 'provider' END WHERE id IN (?,?)`, a, a, b); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEdge(protocol.Edge{From: "*", To: "provider", Kind: "depends", Rule: "ask"}, "owner"); err != nil {
		t.Fatal(err)
	}
	return s, c, a, b
}

// The Desk reads one list. An owner question, an approval and a session that
// waits are all in it with the shape of a delivery step.
func TestListAsksIsOneOpenList(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	named, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "named", Name: "Builder"})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: named, Node: "demo", Text: "Merge PR #3?", Recommendation: "yes, CI is green", Options: []string{"yes", "no"}, AboutKind: protocol.AskAboutProject, AboutID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: "s_gone", Text: "delete the cache"}); err != nil {
		t.Fatal(err)
	}
	gone, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: "s_other", Text: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInbox(gone.ID, "dismiss"); err != nil {
		t.Fatal(err)
	}
	asks, err := s.ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	if len(asks) != 2 {
		t.Fatalf("want 2 open asks, got %+v", asks)
	}
	// The test clock does not move, so the order of equal times is by id.
	a, other := asks[0], asks[1]
	if a.ID != q.ID {
		a, other = asks[1], asks[0]
	}
	if a.ID != q.ID || a.Kind != "question" || a.From != "Builder" || a.FromKind != protocol.AskFromSession || a.FromSession != named ||
		a.Question != "Merge PR #3?" || a.Recommendation != "yes, CI is green" || len(a.Options) != 2 ||
		a.Next != protocol.AskNextOwner || a.State != protocol.AskStateOpen || a.AboutKind != protocol.AskAboutProject || a.AboutID != "demo" {
		t.Fatalf("ask shape: %+v", a)
	}
	if other.From != "an agent" {
		t.Fatalf("unnamed asker: %+v", other)
	}
}

// An ask whose next is not the owner is open but with someone else, so a later
// chief of staff can hold it.
func TestAskNextAllowsChief(t *testing.T) {
	s, _ := newTestStore(t)
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: "s_x", Text: "q", NextWho: protocol.AskNextChief})
	if err != nil {
		t.Fatal(err)
	}
	asks, _ := s.ListAsks()
	if len(asks) != 1 || asks[0].ID != item.ID || asks[0].Next != protocol.AskNextChief || asks[0].State != protocol.AskWithSomeone {
		t.Fatalf("asks: %+v", asks)
	}
}

// Withdrawing an ask (replace with empty text) and answering it both take it
// off the list.
func TestAskAnsweredAndWithdrawnLeaveTheList(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	_, _ = s.UpsertSession(node, owner("box"))
	sid, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	a, _ := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: sid, Text: "first?"})
	if _, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: sid, Text: "", Replaces: a.ID}); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(asks) != 0 {
		t.Fatalf("withdrawn ask still listed: %+v", asks)
	}
	if status, _, _ := askRow(t, s, `id=?`, a.ID); status != "replaced" {
		t.Fatalf("status %q", status)
	}
	b, _ := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: sid, Text: "second?"})
	if _, err := s.AnswerInbox(b.ID, "yes"); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(asks) != 0 {
		t.Fatalf("answered ask still listed: %+v", asks)
	}
}

// A held request is an ask from its sender, about the session it wants to
// reach. Deciding it closes the ask; so does its expiry.
func TestHeldRequestIsAnAsk(t *testing.T) {
	s, c, a, b := heldRig(t)
	m, _, err := send(s, a, b, "k1", "")
	if err != nil || m.State != protocol.StateHeld {
		t.Fatalf("message %+v %v", m, err)
	}
	asks, _ := s.ListAsks()
	held := askByKind(asks, "held")
	if len(held) != 1 || held[0].Ref != "held:"+m.ID || held[0].FromSession != a || held[0].From != "alice" ||
		held[0].AboutKind != protocol.AskAboutSession || held[0].AboutID != b || held[0].Detail != "hi" {
		t.Fatalf("held asks: %+v", asks)
	}
	if _, err := s.AnswerHeld(m.ID, "a"); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(asks) != 0 {
		t.Fatalf("decided held request still listed: %+v", asks)
	}
	// A second one expires without a decision.
	m2, _, err := send(s, a, b, "k2", "")
	if err != nil || m2.State != protocol.StateHeld {
		t.Fatal(m2, err)
	}
	if asks, _ := s.ListAsks(); len(askByKind(asks, "held")) != 1 {
		t.Fatalf("second held ask missing: %+v", asks)
	}
	c.t = c.t.Add(protocol.DefaultTTL + time.Hour)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(askByKind(asks, "held")) != 0 {
		t.Fatalf("expired held request still listed: %+v", asks)
	}
	status, reason, _ := askRow(t, s, `source_ref=?`, "held:"+m2.ID)
	if status != "closed" || reason != protocol.AskReasonExpired {
		t.Fatalf("expired ask: %s %s", status, reason)
	}
}

// The reconcile creates the held ask for a request that was held before this
// version, and an ask that has no source any more closes.
func TestReconcileOpensHeldAskForOldRequest(t *testing.T) {
	s, _, a, b := heldRig(t)
	m, _, _ := send(s, a, b, "k1", "")
	if _, err := s.db.Exec(`DELETE FROM inbox_items WHERE source_ref=?`, "held:"+m.ID); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(asks) != 0 {
		t.Fatalf("setup: %+v", asks)
	}
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(askByKind(asks, "held")) != 1 {
		t.Fatalf("held ask not opened exactly once: %+v", asks)
	}
}

// An escalation or rejection is an ask from the sender until the owner decides.
func TestOutcomeIsAnAsk(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	req, _, _ := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "do the thing"})
	rep, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "no", ParentID: req.ID, ReplyOutcome: protocol.ReplyOutcome{Outcome: "escalate", Reason: "unclear", Recommendation: "ask owner"}})
	if err != nil {
		t.Fatal(err)
	}
	asks, _ := s.ListAsks()
	esc := askByKind(asks, "escalation")
	if len(esc) != 1 || esc[0].MessageID != rep.ID || !strings.Contains(esc[0].Question, "do the thing") || esc[0].Recommendation != "ask owner" {
		t.Fatalf("escalation asks: %+v", asks)
	}
}

// A session that reports a wait has an ask while it waits.
func TestWaitingSessionIsAnAsk(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	info := protocol.SessionInfo{Harness: "claude", NativeID: "w", Name: "waiter", Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: "Which database?"}}
	id, err := s.UpsertSession(node, info)
	if err != nil {
		t.Fatal(err)
	}
	// The ask comes once the wait outlived the debounce.
	c.t = c.t.Add(waitingAskDebounce)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	asks, _ := s.ListAsks()
	w := askByKind(asks, "waiting")
	if len(w) != 1 || w[0].Question != "Which database?" || w[0].From != "waiter" || w[0].Ref != "waiting:"+id || w[0].AboutID != id {
		t.Fatalf("waiting asks: %+v", asks)
	}
	// The same wait reported again is still one ask.
	if _, err := s.UpsertSession(node, info); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(askByKind(asks, "waiting")) != 1 {
		t.Fatalf("duplicate wait ask: %+v", asks)
	}
	// An explicit wait comes from agent_owner_ask, which has its own ask.
	info.Waiting = &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "Which database?", Explicit: true}
	if _, err := s.UpsertSession(node, info); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(askByKind(asks, "waiting")) != 0 {
		t.Fatalf("explicit wait made a second ask: %+v", asks)
	}
	info.Waiting = &protocol.Waiting{Kind: protocol.WaitingInput, Text: "Which database?"}
	if _, err := s.UpsertSession(node, info); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(waitingAskDebounce)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	info.Waiting = nil
	if _, err := s.UpsertSession(node, info); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(askByKind(asks, "waiting")) != 0 {
		t.Fatalf("wait ended but ask stays: %+v", asks)
	}
}

// An ask closes when its asker's session ends, and keeps the reason.
func TestAskClosesWhenAskerSessionEnds(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	sid, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	q, _ := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: sid, Text: "q?"})
	chk, _ := s.CreateInboxItem(protocol.InboxItem{Kind: "check", SessionID: sid, Text: "x is blocked"})
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(asks) != 2 {
		t.Fatalf("live session closed its asks: %+v", asks)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET archived_at=? WHERE id=?`, s.now().UnixNano(), sid); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(asks) != 0 {
		t.Fatalf("asks of an ended session: %+v", asks)
	}
	for _, id := range []string{q.ID, chk.ID} {
		if status, reason, _ := askRow(t, s, `id=?`, id); status != "closed" || reason != protocol.AskReasonSessionEnded {
			t.Fatalf("ask %s: %s %s", id, status, reason)
		}
	}
	log, _ := s.DeskLog(10)
	if len(log) != 2 || !strings.Contains(log[0].Text, "session ended") {
		t.Fatalf("log: %+v", log)
	}
}

// A request between sessions is not the asker's: it stays when the sender ends.
func TestHeldAskStaysWhenSenderEnds(t *testing.T) {
	s, _, a, b := heldRig(t)
	send(s, a, b, "k", "")
	if _, err := s.db.Exec(`UPDATE sessions SET archived_at=? WHERE id=?`, s.now().UnixNano(), a); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(askByKind(asks, "held")) != 1 {
		t.Fatalf("held ask closed with the sender: %+v", asks)
	}
}

// A step that needs the owner is an ask about its change, shown by that
// change's row. A later report for the step, or a finished change, closes it.
func TestStepNeedsOwnerAskAboutChange(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	pass := func(ref protocol.StepRef, commit string, fields map[string]string) {
		t.Helper()
		r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: ref, Result: protocol.StepResultPass, Commit: commit, Fields: fields})
	}
	pass(stepAccept, commitA, nil)
	pass(protocol.StepRef{Step: protocol.StepTest}, commitA, map[string]string{protocol.FieldSuite: protocol.SuiteChanged})
	pass(protocol.StepRef{Step: protocol.StepMerge}, commitA, map[string]string{protocol.FieldMergedCommit: commitMerged})
	pass(protocol.StepRef{Step: protocol.StepTest, AfterMerge: true}, commitMerged, map[string]string{protocol.FieldSuite: protocol.SuiteFull})
	pass(protocol.StepRef{Step: protocol.StepRelease, AfterMerge: true}, commitMerged, map[string]string{protocol.FieldTag: "v1.2.3"})
	verify := protocol.StepRef{Step: protocol.StepVerify, AfterMerge: true}
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: verify,
		Result: protocol.StepResultNeedsOwner, Commit: commitMerged, Note: "Owner: press v and try it"})
	asks, err := r.s.ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	steps := askByKind(asks, "step")
	if len(steps) != 1 || steps[0].AboutKind != protocol.AskAboutChange || steps[0].AboutID != id || steps[0].Question != "Owner: press v and try it" {
		t.Fatalf("step asks: %+v", asks)
	}
	// The same report again does not add a second ask.
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: verify,
		Result: protocol.StepResultNeedsOwner, Commit: commitMerged, Note: "Owner: press v and try it"})
	if asks, _ := r.s.ListAsks(); len(askByKind(asks, "step")) != 1 {
		t.Fatalf("repeat made a second ask: %+v", asks)
	}
	pass(verify, commitMerged, nil)
	if asks, _ := r.s.ListAsks(); len(askByKind(asks, "step")) != 0 {
		t.Fatalf("answered step ask stays: %+v", asks)
	}
}

// An ask about a change closes by itself when the change is done or stopped.
func TestAskClosesWhenItsChangeIsDone(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	q, err := r.s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: r.caller, Text: "ship it?", AboutKind: protocol.AskAboutChange, AboutID: id})
	if err != nil {
		t.Fatal(err)
	}
	if asks, _ := r.s.ListAsks(); len(asks) != 1 || asks[0].AboutKind != protocol.AskAboutChange || asks[0].AboutID != id {
		t.Fatalf("asks: %+v", asks)
	}
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultClose, Commit: commitA, Note: "not wanted"})
	if asks, _ := r.s.ListAsks(); len(asks) != 0 {
		t.Fatalf("ask about a stopped change stays: %+v", asks)
	}
	if status, reason, _ := askRow(t, r.s, `id=?`, q.ID); status != "closed" || reason != protocol.AskReasonChangeDone {
		t.Fatalf("ask: %s %s", status, reason)
	}
}

// What the CTO applied or sent is a line in the log with its undo, not an ask.
// A new app is a line in the log too.
func TestNoticesGoToTheLogNotTheQueue(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	cto, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s}
	raw, _ := json.Marshal(map[string]any{"session_id": cto, "kind": "check", "text": "CTO applied mcp add github", "recommendation": "diff", "options": []string{"mcp_remove", `{"name":"github"}`}})
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "inbox.create", raw); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(asks) != 0 {
		t.Fatalf("CTO notice in the queue: %+v", asks)
	}
	// No Inbox row is written for it any more.
	if old, _ := s.ListInbox(); len(old) != 0 {
		t.Fatalf("CTO notice in the old inbox: %+v", old)
	}
	log, err := s.DeskLog(10)
	if err != nil || len(log) != 1 || log[0].Source != "cto" || log[0].Text != "CTO applied mcp add github" || len(log[0].Options) != 2 || log[0].Options[0] != "mcp_remove" {
		t.Fatalf("log: %+v %v", log, err)
	}
	// Disk low stays an ask, from the disk check.
	raw, _ = json.Marshal(map[string]any{"session_id": node, "kind": "check", "text": "Disk space low on box: 91% used"})
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "inbox.create", raw); err != nil {
		t.Fatal(err)
	}
	asks, _ := s.ListAsks()
	if len(asks) != 1 || asks[0].FromKind != protocol.AskFromDisk {
		t.Fatalf("disk ask: %+v", asks)
	}
}

// A daemon report needs no answer: it is one line in the event log.
func TestDeskNoteAppendsAReport(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	raw, _ := json.Marshal(protocol.DeskNote{Text: "garden on research: wheel off (mouse tracking disabled); it scrolls after its next restart"})
	if _, err := g.handle(context.Background(), &nodeConn{id: "research"}, "desk.note", raw); err != nil {
		t.Fatal(err)
	}
	log, err := s.DeskLog(5)
	if err != nil || len(log) != 1 {
		t.Fatalf("log: %+v %v", log, err)
	}
	if log[0].Source != "daemon" || log[0].AboutKind != "node" || log[0].AboutID != "research" || !strings.Contains(log[0].Text, "wheel off") {
		t.Fatalf("entry: %+v", log[0])
	}
	if _, err := g.handle(context.Background(), &nodeConn{id: "research"}, "desk.note", []byte(`{"detail":"x"}`)); err == nil {
		t.Fatal("empty text accepted")
	}
}

// The log lists newest first, caps its size, and the RPC returns it.
func TestDeskLogNewestFirstAndBounded(t *testing.T) {
	s, _ := newTestStore(t)
	for i := 0; i < 450; i++ {
		if err := s.deskLog(s.db, "gateway", "", "", "line "+string(rune('a'+i%26)), "", nil); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM desk_log`).Scan(&n); err != nil || n > deskLogKeep {
		t.Fatalf("log rows %d %v", n, err)
	}
	log, err := s.DeskLog(0)
	if err != nil || len(log) != deskLogDefault || log[0].ID < log[1].ID {
		t.Fatalf("log: %d %v", len(log), err)
	}
	g := &Gateway{store: s}
	out, err := g.handle(context.Background(), &nodeConn{id: "n"}, "desk.log", []byte(`{"limit":5}`))
	if err != nil {
		t.Fatal(err)
	}
	if res, ok := out.(protocol.DeskLogResult); !ok || len(res.Entries) != 5 {
		t.Fatalf("rpc: %#v", out)
	}
}

// A source outside the gateway (the daemon for a CTO proposal) opens and
// closes its ask by key, safely repeated.
func TestExternalAskOpenAndClose(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	open, _ := json.Marshal(protocol.AskOpen{Kind: protocol.AskKindProposal, Ref: "7", From: "CTO", Question: "Archive 3 sessions?", Options: []string{"apply", "decline"}, Recommendation: "apply"})
	for i := 0; i < 2; i++ {
		if _, err := g.handle(context.Background(), &nodeConn{id: "n1"}, "ask.open", open); err != nil {
			t.Fatal(err)
		}
	}
	asks, _ := s.ListAsks()
	if len(asks) != 1 || asks[0].Kind != "proposal" || asks[0].FromKind != protocol.AskFromCTO || asks[0].From != "CTO" || len(asks[0].Options) != 2 {
		t.Fatalf("asks: %+v", asks)
	}
	cl, _ := json.Marshal(protocol.AskClose{Ref: "7", Reason: protocol.AskReasonResolved})
	if _, err := g.handle(context.Background(), &nodeConn{id: "n1"}, "ask.close", cl); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(asks) != 0 {
		t.Fatalf("closed proposal ask stays: %+v", asks)
	}
	// Another machine's key does not close it.
	if _, err := g.handle(context.Background(), &nodeConn{id: "n1"}, "ask.open", open); err != nil {
		t.Fatal(err)
	}
	if _, err := g.handle(context.Background(), &nodeConn{id: "n2"}, "ask.close", cl); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(asks) != 1 {
		t.Fatalf("a close from another machine closed the ask: %+v", asks)
	}
}

// A ready major learned suggestion and a pending charter draft are asks with
// options; deciding them closes the asks.
func TestLearnedAndDraftAsks(t *testing.T) {
	s, c := newTestStore(t)
	now := s.now().UnixNano()
	res, err := s.db.Exec(`INSERT INTO learned_suggestions(node,field,edge_kind,kind,phrase,evidence,status,severity,created_at,ready_at) VALUES('demo','accepts','','add','billing','[]','pending','major',?,?)`, now, now+int64(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(asks) != 0 {
		t.Fatalf("suggestion not ready yet is an ask: %+v", asks)
	}
	c.t = c.t.Add(3 * time.Minute)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	asks, _ := s.ListAsks()
	l := askByKind(asks, "learned")
	if len(l) != 1 || l[0].Ref != "learned:"+itoa(id) || len(l[0].Options) != 2 || !strings.Contains(l[0].Question, "billing") || l[0].AboutID != "demo" {
		t.Fatalf("learned asks: %+v", asks)
	}
	if _, err := s.db.Exec(`UPDATE learned_suggestions SET status='rejected' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(asks) != 0 {
		t.Fatalf("decided suggestion still an ask: %+v", asks)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// The asks migration adds the columns and keeps every open item. CTO and app
// notices that were open are copied to the log and are not asks; a delivery
// approval gets its change as the thing it is about.
func TestAsksMigrationKeepsOpenItems(t *testing.T) {
	s, _ := newTestStore(t)
	version := migrationVersion(t, "Asks and the Desk (decision 0090)")
	for _, stmt := range []string{
		`DROP TRIGGER inbox_items_desk_insert`, `DROP TRIGGER inbox_items_desk_update`, `DROP TRIGGER inbox_items_joined_settle`,
		`DROP INDEX inbox_items_open_source`, `DROP INDEX inbox_items_status`, `DROP TABLE desk_log`,
		`ALTER TABLE inbox_items DROP COLUMN from_kind`, `ALTER TABLE inbox_items DROP COLUMN source_ref`,
		`ALTER TABLE inbox_items DROP COLUMN about_kind`, `ALTER TABLE inbox_items DROP COLUMN about_id`,
		`ALTER TABLE inbox_items DROP COLUMN next_who`, `ALTER TABLE inbox_items DROP COLUMN why_you`,
		`ALTER TABLE inbox_items DROP COLUMN detail`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	now := s.now().UnixNano()
	ins := func(id, kind, session, node, text, msg, status string) {
		t.Helper()
		if _, err := s.db.Exec(`INSERT INTO inbox_items(id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,options,asker_name,change_title) VALUES(?,?,?,?,?, 'rec',?, '',?,?, '["a","b"]','Name','')`,
			id, kind, session, node, text, msg, now, status); err != nil {
			t.Fatal(err)
		}
	}
	ins("i_q", "question", "s_1", "demo", "Merge?", "", "open")
	ins("i_old", "question", "s_1", "demo", "answered long ago", "", "answered")
	ins("i_cto", "check", "s_cto", "cto", "CTO applied mcp add", "", "open")
	ins("i_app", "check", "s_o", "apps", "Connected: Phone", "app-approval:x", "open")
	ins("i_disk", "check", "n1", "box", "Disk space low on box: 91% used", "", "open")
	ins("i_appr", "approval", "s_1", "demo", "Approve for change ch_1", "", "open")
	if _, err := s.db.Exec(`INSERT INTO delivery_changes(id,project,machine,branch,head_commit,revision,origin,title,state,created_at,updated_at) VALUES('ch_1','demo','n1','b','abc',1,'{}','Title','open',0,0)`); err != nil {
		t.Skipf("cannot seed a change on this schema: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO delivery_step_inbox(change_id,revision,step,for_step,after_merge,commit_hash,item_id) VALUES('ch_1',1,'approve','merge',0,'abc','i_appr')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, version); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // a replay must be safe
		if err := s.migrate(); err != nil {
			t.Fatal(err)
		}
	}
	asks, err := s.ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]protocol.Ask{}
	for _, a := range asks {
		got[a.ID] = a
	}
	// The Desk reroute (decision 0152) runs after the asks migration, so an
	// owner-bound question with no chief sort now waits for the chief.
	if len(got) != 3 || got["i_q"].Question != "Merge?" || got["i_q"].AboutKind != protocol.AskAboutProject || got["i_q"].AboutID != "demo" || got["i_q"].Next != protocol.AskNextChief ||
		got["i_disk"].FromKind != protocol.AskFromDisk || got["i_appr"].AboutKind != protocol.AskAboutChange || got["i_appr"].AboutID != "ch_1" {
		t.Fatalf("asks after upgrade: %+v", asks)
	}
	// The notices are not asks. They are copied to the log once, even when the
	// migration runs twice, and their old Inbox rows are closed, not deleted.
	for _, id := range []string{"i_cto", "i_app"} {
		if status, reason, _ := askRow(t, s, `id=?`, id); status != "closed" || reason != protocol.AskReasonMovedToLog {
			t.Fatalf("%s: %s %s", id, status, reason)
		}
	}
	log, _ := s.DeskLog(10)
	if len(log) != 2 {
		t.Fatalf("notices missing from the log: %+v", log)
	}
}

// A daemon sends its whole list of open proposals; the gateway opens the new
// ones and closes the ones that are gone, so a missed close cannot linger.
func TestExternalAskSyncRepairs(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	sync := func(node string, refs ...string) {
		t.Helper()
		p := protocol.AskSync{Kind: protocol.AskKindProposal}
		for _, r := range refs {
			p.Asks = append(p.Asks, protocol.AskOpen{Ref: r, From: "CTO", Question: "proposal " + r})
		}
		raw, _ := json.Marshal(p)
		if _, err := g.handle(context.Background(), &nodeConn{id: node}, "ask.sync", raw); err != nil {
			t.Fatal(err)
		}
	}
	sync("n1", "1", "2")
	sync("n2", "9")
	if asks, _ := s.ListAsks(); len(asks) != 3 {
		t.Fatalf("asks: %+v", asks)
	}
	sync("n1", "2", "3")
	asks, _ := s.ListAsks()
	got := map[string]bool{}
	for _, a := range asks {
		got[a.Question] = true
	}
	if len(asks) != 3 || !got["proposal 2"] || !got["proposal 3"] || !got["proposal 9"] || got["proposal 1"] {
		t.Fatalf("after sync: %+v", asks)
	}
}

// "Blocked on" and "stopped with work left" asks close by themselves when the
// session starts new work.
func TestBlockedAskClosesWhenSessionMovesOn(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check = %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	node := mustNode(t, s, b)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: b, Task: "blocked on the deploy key"}); err != nil {
		t.Fatal(err)
	}
	asks, _ := s.ListAsks()
	if chk := askByKind(asks, "check"); len(chk) != 1 || !strings.Contains(chk[0].Question, "the deploy key") {
		t.Fatalf("blocked ask: %+v", asks)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: b, Task: "fixing the CI config"}); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.ListAsks(); len(askByKind(asks, "check")) != 0 {
		t.Fatalf("blocked ask stays after new work: %+v", asks)
	}
}

// What the gateway sends by itself is written to the log.
func TestStepDispatchWritesToTheLog(t *testing.T) {
	r := setupMergerRig(t)
	id := r.submit(r.a, r.workerSubmit("feature/logged", commitA)).Change.Change.ID
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTaskBatches()
	log, err := r.s.DeskLog(20)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range log {
		if e.Source == "gateway" && e.AboutKind == protocol.AskAboutChange && e.AboutID == id && strings.Contains(e.Text, "Sent step") {
			return
		}
	}
	t.Fatalf("no dispatch line for %s in the log: %+v", id, log)
}

// A question or an approval the session raised before its earlier announce is
// overtaken by a later announce without a wait: the Needs you tag goes. An ask
// of the same turn as the announce stays, and so does one under a new wait.
func TestAnnounceClosesAskOfEarlierWork(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	sid, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	announce := func(task string, w *protocol.Waiting) {
		t.Helper()
		c.t = c.t.Add(time.Minute)
		if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: sid, Task: task, Waiting: w}); err != nil {
			t.Fatal(err)
		}
	}
	announce("build the grant", nil)
	c.t = c.t.Add(time.Minute)
	old, _ := s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: sid, Text: "Build the grant?"})
	announce("waiting for the owner", nil) // same turn as the ask: it stays
	if status, _, _ := askRow(t, s, `id=?`, old.ID); status != "open" {
		t.Fatalf("an ask of the same turn closed: %s", status)
	}
	announce("waiting for the owner", &protocol.Waiting{Kind: protocol.WaitingInput, Text: "Build the grant?"})
	if status, _, _ := askRow(t, s, `id=?`, old.ID); status != "open" {
		t.Fatalf("an announce with a wait closed the ask: %s", status)
	}
	announce("doing the next thing", nil)
	status, reason, _ := askRow(t, s, `id=?`, old.ID)
	if status != "closed" || reason != protocol.AskReasonMovedOn {
		t.Fatalf("ask after new work: %s %s", status, reason)
	}
}

// closeSessionOwnerAsks closes the asks the session raised, never another
// session's, never a delivery step, and writes the reason.
func TestOwnerTypedClosesOnlyThatSessionsAsks(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	a, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "a"})
	b, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "b"})
	qa, _ := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: a, Text: "q?"})
	ka, _ := s.CreateInboxItem(protocol.InboxItem{Kind: "check", SessionID: a, Text: "stopped with work left: x"})
	ia, _ := s.CreateInboxItem(protocol.InboxItem{Kind: "feature_idea", SessionID: a, Text: "an idea"})
	qb, _ := s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: b, Text: "ok?"})
	if n, err := s.closeSessionOwnerAsks(s.db, a, 0, protocol.AskReasonOwnerReplied); err != nil || n != 2 {
		t.Fatalf("closed %d: %v", n, err)
	}
	for id, want := range map[string]string{qa.ID: "closed", ka.ID: "closed", ia.ID: "open", qb.ID: "open"} {
		if status, _, _ := askRow(t, s, `id=?`, id); status != want {
			t.Fatalf("ask %s = %s, want %s", id, status, want)
		}
	}
}

// A "stopped with work left" row stays no longer than the session's next
// announce, also when that announce does not answer a pending idle check.
func TestStoppedWithWorkLeftRowClosesOnNewWorkAnnounce(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	sid, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	row, _ := s.CreateInboxItem(protocol.InboxItem{Kind: "check", SessionID: sid, Text: "p stopped with work left: x"})
	if _, err := s.db.Exec(`INSERT INTO session_checks(session_id,check_active,check_waiting) VALUES(?,?,1)`, sid, c.t.Unix()); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: sid, Task: "fixing the CI config"}); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := askRow(t, s, `id=?`, row.ID); status != "closed" {
		t.Fatalf("row after a new-work announce: %s", status)
	}
}

// The one-time cleanup closes the approvals and check items a session left
// behind: it started a task long after the item. An item of the same turn, a
// session that stayed on its task, a question, a second open approval of the
// same session and other sessions' items stay. It never runs twice.
func TestMovedOnCleanupRunsOnceAndIsConservative(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	moved, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "moved"})
	same, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "same"})
	check, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "check"})
	two, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "two"})
	if _, err := s.db.Exec(`DELETE FROM owner_settings WHERE key=?`, movedOnCleanupKey); err != nil {
		t.Fatal(err)
	}
	// moved: an approval and a question, then a task announced 11 minutes later.
	oldApproval, _ := s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: moved, Text: "Build the grant?"})
	oldQuestion, _ := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: moved, Text: "Which iOS version?"})
	// same: an approval and a task line 2 minutes later.
	keep, _ := s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: same, Text: "Merge?"})
	// check: a check row of a session that moved on.
	stale, _ := s.CreateInboxItem(protocol.InboxItem{Kind: "check", SessionID: check, Text: "stopped with work left: a"})
	// two: two different approvals open at once, and no later task.
	first, _ := s.createInboxItem(s.db, protocol.InboxItem{Kind: "approval", SessionID: two, Text: "Create the ops token"})
	c.t = c.t.Add(2 * time.Minute)
	second, _ := s.createInboxItem(s.db, protocol.InboxItem{Kind: "approval", SessionID: two, Text: "Merge #905"})
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: same, Task: "waiting for the owner"}); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(10 * time.Minute)
	for _, id := range []string{moved, check} {
		if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: id, Task: "next thing"}); err != nil {
			t.Fatal(err)
		}
	}
	// The announce rule would already have closed these; open them again, as
	// the live gateway has them from before the rule.
	if _, err := s.db.Exec(`UPDATE inbox_items SET status='open',answer='',answered_at=0 WHERE id IN (?,?,?)`, oldApproval.ID, oldQuestion.ID, stale.ID); err != nil {
		t.Fatal(err)
	}
	n, err := s.reconcileMovedOnOnce()
	if err != nil || n != 2 {
		t.Fatalf("closed %d: %v", n, err)
	}
	for id, want := range map[string]string{oldApproval.ID: "closed", stale.ID: "closed", oldQuestion.ID: "open", keep.ID: "open", first.ID: "open", second.ID: "open"} {
		if status, _, _ := askRow(t, s, `id=?`, id); status != want {
			t.Fatalf("ask %s = %s, want %s", id, status, want)
		}
	}
	if _, reason, _ := askRow(t, s, `id=?`, oldApproval.ID); reason != protocol.AskReasonMovedOnClean {
		t.Fatalf("reason %q", reason)
	}
	// Run again: the marker stops it, even for a new match.
	if _, err := s.db.Exec(`UPDATE inbox_items SET status='open',answer='' WHERE id=?`, oldApproval.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.reconcileMovedOnOnce(); err != nil || n != 0 {
		t.Fatalf("second run closed %d: %v", n, err)
	}
}

// An escalation closes when the session that escalated later answers the
// request with a plain reply. A later outcome reply does not count.
func TestEscalationClosesWhenTheRequestIsAnsweredLater(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	req, _, err := send(s, a, b, "req", "")
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Minute)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "unclear", ParentID: req.ID, ReplyOutcome: protocol.ReplyOutcome{Outcome: "escalate", Reason: "unclear", Recommendation: "ask owner"}}); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := s.db.QueryRow(`SELECT id FROM inbox_items WHERE kind='escalation'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if n, err := s.reconcileEscalationAsks(s.db); err != nil || n != 0 {
		t.Fatalf("closed %d before any answer: %v", n, err)
	}
	c.t = c.t.Add(time.Minute)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "done after the owner approved", ParentID: req.ID}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.reconcileEscalationAsks(s.db); err != nil || n != 1 {
		t.Fatalf("closed %d after the answer: %v", n, err)
	}
	if status, reason, _ := askRow(t, s, `id=?`, id); status != "closed" || reason != protocol.AskReasonAnswered {
		t.Fatalf("escalation: %s %s", status, reason)
	}
	var resolution string
	if err := s.db.QueryRow(`SELECT resolution FROM graph_outcomes WHERE message_id=?`, req.ID).Scan(&resolution); err != nil || resolution != "answered" {
		t.Fatalf("outcome resolution %q: %v", resolution, err)
	}
}

// An undone log line cannot be undone twice.
func TestSettleDeskLog(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.deskLog(s.db, "cto", "", "", "CTO applied graph edge", "", []string{"graph_undo", `{"id":1}`}); err != nil {
		t.Fatal(err)
	}
	log, _ := s.DeskLog(5)
	for i := 0; i < 2; i++ {
		if err := s.SettleDeskLog(log[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	log, _ = s.DeskLog(5)
	if len(log) != 1 || len(log[0].Options) != 0 || log[0].Text != "Undone: CTO applied graph edge" {
		t.Fatalf("log: %+v", log)
	}
}

// A learned suggestion reads in the owner's words, with the owner's own words
// when the owner said them.
func TestLearnedAskWording(t *testing.T) {
	cases := []struct {
		name string
		s    protocol.LearnedSuggestion
		want string
	}{
		{"add owner words", protocol.LearnedSuggestion{Node: "ops", Field: "refuses", Kind: "add", Phrase: "deployment work", RawPhrase: "deploys", Origin: "owner"}, `add "deploys" to what it refuses`},
		{"remove", protocol.LearnedSuggestion{Node: "app", Field: "accepts", Kind: "remove", Phrase: "backups"}, `remove "backups" from what it accepts`},
		{"edge", protocol.LearnedSuggestion{Node: "ops", Field: "edge", EdgeKind: "deploys", Kind: "add", Phrase: "app"}, "add a deploys edge to app"},
	}
	for _, tc := range cases {
		if got := learnedAskText(tc.s); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
