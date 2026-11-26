package gateway

import (
	"context"
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
	q, err := storeOldItem(t, s, protocol.InboxItem{Kind: "question", SessionID: named, Node: "demo", Text: "Merge PR #3?", Recommendation: "yes, CI is green", Options: []string{"yes", "no"}, AboutKind: protocol.AskAboutProject, AboutID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storeOldItem(t, s, protocol.InboxItem{Kind: "approval", SessionID: "s_gone", Text: "delete the cache"}); err != nil {
		t.Fatal(err)
	}
	gone, err := storeOldItem(t, s, protocol.InboxItem{Kind: "question", SessionID: "s_other", Text: "old"})
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
	item, err := storeOldItem(t, s, protocol.InboxItem{Kind: "question", SessionID: "s_x", Text: "q", NextWho: protocol.AskNextChief})
	if err != nil {
		t.Fatal(err)
	}
	asks, _ := s.ListAsks()
	if len(asks) != 1 || asks[0].ID != item.ID || asks[0].Next != protocol.AskNextChief || asks[0].State != protocol.AskWithSomeone {
		t.Fatalf("asks: %+v", asks)
	}
}

// An ask closes when its asker's session ends, and keeps the reason.
func TestAskClosesWhenAskerSessionEnds(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	sid, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	q, _ := storeOldItem(t, s, protocol.InboxItem{Kind: "question", SessionID: sid, Text: "q?"})
	chk, _ := storeOldItem(t, s, protocol.InboxItem{Kind: "check", SessionID: sid, Text: "x is blocked"})
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

// An ask about a change closes by itself when the change is done or stopped.
func TestAskClosesWhenItsChangeIsDone(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	q, err := storeOldItem(t, r.s, protocol.InboxItem{Kind: "question", SessionID: r.caller, Text: "ship it?", AboutKind: protocol.AskAboutChange, AboutID: id})
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

// The asks migration adds the columns and keeps every open item. CTO and app
// notices that were open are copied to the log and are not asks; a delivery
// approval gets its change as the thing it is about.
func TestAsksMigrationKeepsOpenItems(t *testing.T) {
	s, _ := newTestStore(t)
	version := migrationVersion(t, "Asks and the Desk (decision 0090)")
	for _, stmt := range []string{
		`DROP TRIGGER IF EXISTS inbox_items_desk_insert`, `DROP TRIGGER IF EXISTS inbox_items_desk_update`, `DROP TRIGGER IF EXISTS inbox_items_joined_settle`,
		`DROP INDEX IF EXISTS inbox_items_open_source`, `DROP INDEX IF EXISTS inbox_items_status`, `DROP TABLE desk_log`,
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
	recreateDeskDroppedTables(t, s.db)
	if _, err := s.db.Exec(`INSERT INTO delivery_step_inbox(change_id,revision,step,for_step,after_merge,commit_hash,item_id) VALUES('ch_1',1,'approve','merge',0,'abc','i_appr')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, version); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // a replay must be safe
		if err := replayMigrations(t, s); err != nil {
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
	// The front door flush (plan step (d)) ran once when this store was made,
	// so this replay leaves the owner question open and with the owner.
	if len(got) != 3 || got["i_q"].Question != "Merge?" || got["i_q"].AboutKind != protocol.AskAboutProject || got["i_q"].AboutID != "demo" || got["i_q"].Next != protocol.AskNextOwner ||
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

// A question the session raised before its earlier announce is overtaken by a
// later announce without a wait: the Needs you tag goes. An ask of the same
// turn as the announce stays, and so does one under a new wait.
func TestAnnounceClosesAskOfEarlierWork(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	sid, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	announce := func(task string, w *protocol.Waiting) {
		t.Helper()
		c.advance(time.Minute)
		if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: sid, Task: task, Waiting: w}); err != nil {
			t.Fatal(err)
		}
	}
	announce("build the grant", nil)
	c.advance(time.Minute)
	old, _ := storeOldItem(t, s, protocol.InboxItem{Kind: "question", SessionID: sid, Text: "Which region for the new box?"})
	announce("waiting for the owner", nil) // same turn as the ask: it stays
	if status, _, _ := askRow(t, s, `id=?`, old.ID); status != "open" {
		t.Fatalf("an ask of the same turn closed: %s", status)
	}
	announce("waiting for the owner", &protocol.Waiting{Kind: protocol.WaitingInput, Text: "Which region for the new box?"})
	if status, _, _ := askRow(t, s, `id=?`, old.ID); status != "open" {
		t.Fatalf("an announce with a wait closed the ask: %s", status)
	}
	announce("doing the next thing", nil)
	status, reason, _ := askRow(t, s, `id=?`, old.ID)
	if status != "closed" || reason != protocol.AskReasonMovedOn {
		t.Fatalf("ask after new work: %s %s", status, reason)
	}
}

// An announce without a wait never closes an approval: only the owner's
// answer, the asker's withdraw or the chief's replace-withdraw does.
func TestAnnounceKeepsAnApprovalOpen(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	sid, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	announce := func(task string) {
		t.Helper()
		c.advance(time.Minute)
		if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: sid, Task: task}); err != nil {
			t.Fatal(err)
		}
	}
	announce("build the grant")
	approval, _ := storeOldItem(t, s, protocol.InboxItem{Kind: "approval", SessionID: sid, Text: "Approve merge #935 at 2143e3af"})
	// The announce after that one has the approval before its earlier
	// announce, the case that closed it moved_on in the live gateway.
	announce("waiting for the owner")
	announce("doing the next thing")
	if status, reason, _ := askRow(t, s, `id=?`, approval.ID); status != "open" {
		t.Fatalf("an announce closed the approval: %s %s", status, reason)
	}
}

// closeSessionOwnerAsks closes the asks the session raised, never another
// session's, never a delivery step, and writes the reason.
func TestOwnerTypedClosesOnlyThatSessionsAsks(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	a, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "a"})
	b, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "b"})
	qa, _ := storeOldItem(t, s, protocol.InboxItem{Kind: "question", SessionID: a, Text: "q?"})
	ka, _ := storeOldItem(t, s, protocol.InboxItem{Kind: "check", SessionID: a, Text: "stopped with work left: x"})
	ia, _ := storeOldItem(t, s, protocol.InboxItem{Kind: "feature_idea", SessionID: a, Text: "an idea"})
	qb, _ := storeOldItem(t, s, protocol.InboxItem{Kind: "approval", SessionID: b, Text: "ok?"})
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
	row, _ := storeOldItem(t, s, protocol.InboxItem{Kind: "check", SessionID: sid, Text: "p stopped with work left: x"})
	if _, err := s.db.Exec(`UPDATE sessions SET check_active=?,check_waiting=1 WHERE id=?`, c.t.Unix(), sid); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: sid, Task: "fixing the CI config"}); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := askRow(t, s, `id=?`, row.ID); status != "closed" {
		t.Fatalf("row after a new-work announce: %s", status)
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
