package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// answerRows are the answer judgments of one ask. A second answer of the same
// ask must update the row, so a test expects at most one.
func answerRows(t *testing.T, s *Store, ask string) []protocol.StandingOrder {
	t.Helper()
	rows, err := s.FindJudgments(protocol.JudgmentsFindRequest{Kind: protocol.StandingKindAnswer, Ref: ask})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func oneAnswer(t *testing.T, s *Store, ask string) protocol.StandingOrder {
	t.Helper()
	rows := answerRows(t, s, ask)
	if len(rows) != 1 {
		t.Fatalf("ask %s has %d answer judgments, want 1: %+v", ask, len(rows), rows)
	}
	if rows[0].Ref != ask || rows[0].Source != protocol.StandingSourceOwner {
		t.Fatalf("judgment key: %+v", rows[0])
	}
	return rows[0]
}

func gatewayFind(t *testing.T, g *Gateway, node string, q protocol.JudgmentsFindRequest) []protocol.StandingOrder {
	t.Helper()
	raw, _ := json.Marshal(q)
	res, err := g.handle(context.Background(), &nodeConn{id: node}, "judgment.find", raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("judgment.find result: %T", res)
	}
	items, _ := m["items"].([]protocol.StandingOrder)
	return items
}

// The owner answers through the gateway's inbox.answer frame, the one the TUI,
// the phone, the web console and the CTO all use. The judgment search then
// finds the answer with the ask's ID, whoever answered.
func TestAnswerThroughTheGatewayFrameIsFoundByJudgmentSearch(t *testing.T) {
	for _, actor := range []string{"owner", "cto"} {
		t.Run(actor, func(t *testing.T) {
			s, _ := newTestStore(t)
			node, _, _ := s.CreateNode("test", "linux")
			_, _ = s.UpsertSession(node, owner("test"))
			agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p", Repo: protocol.RepoInfo{Project: "tradedesk"}})
			item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: agent, Text: "May forge merge its own PRs?"})
			if err != nil {
				t.Fatal(err)
			}
			g := &Gateway{store: s}
			raw, _ := json.Marshal(map[string]string{"item_id": item.ID, "answer": "Yes, but never contracts.", "actor": actor})
			if _, err := g.handle(context.Background(), &nodeConn{id: node}, "inbox.answer", raw); err != nil {
				t.Fatal(err)
			}
			found := gatewayFind(t, g, node, protocol.JudgmentsFindRequest{Query: "forge"})
			if len(found) != 1 || found[0].Ref != item.ID || found[0].Kind != protocol.StandingKindAnswer || found[0].Area != "tradedesk" ||
				found[0].Text != "Q: May forge merge its own PRs? / A: Yes, but never contracts." {
				t.Fatalf("found: %+v", found)
			}
		})
	}
}

// A button with no text stores what the button meant, in plain words. The
// ask's own code stays in the answer.
func TestButtonAnswersStoreTheDecisionInPlainWords(t *testing.T) {
	cases := []struct {
		name, kind, text, answer, want, code string
	}{
		{"deploy approved", "approval", "Merge truenas-stacks #885?\nValidate passes; merging deploys ingest-forge.", "approve", "Q: Merge truenas-stacks #885?\nValidate passes; merging deploys ingest-forge. / A: approved: Merge truenas-stacks #885?", "approve"},
		{"deploy refused", "approval", "Deploy the pre-deploy hook?", "refuse", "Q: Deploy the pre-deploy hook? / A: refused: Deploy the pre-deploy hook?", "refuse"},
		{"video dismissed", "check", "video is blocked on owner: delete .cast/.wav files?", "dismiss", "Q: video is blocked on owner: delete .cast/.wav files? / A: dismissed: no decision", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestStore(t)
			node, _, _ := s.CreateNode("test", "linux")
			_, _ = s.UpsertSession(node, owner("test"))
			agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
			item, err := s.CreateInboxItem(protocol.InboxItem{Kind: tc.kind, SessionID: agent, Node: "video", Text: tc.text})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.AnswerInboxAs(item.ID, tc.answer, "cto"); err != nil {
				t.Fatal(err)
			}
			if got := oneAnswer(t, s, item.ID).Text; got != tc.want {
				t.Fatalf("judgment text:\n got %q\nwant %q", got, tc.want)
			}
			var answer string
			if err := s.db.QueryRow(`SELECT answer FROM inbox_items WHERE id=?`, item.ID).Scan(&answer); err != nil || answer != tc.code {
				t.Fatalf("the ask keeps its code %q, got %q (%v)", tc.code, answer, err)
			}
		})
	}
}

func TestCharterDraftAnswerIsIndexed(t *testing.T) {
	s := teamStore(t)
	sevenFieldDrafts(t, s, draftSession(t, s, "demo"))
	asks := draftAsks(t, s)
	if len(asks) != 1 {
		t.Fatalf("draft asks: %+v", asks)
	}
	if _, err := s.AnswerInbox(asks[0].ID, "accept"); err != nil {
		t.Fatal(err)
	}
	want := "Q: Charter draft for demo: 7 fields / A: accepted: Charter draft for demo: 7 fields"
	if got := oneAnswer(t, s, asks[0].ID).Text; got != want {
		t.Fatalf("judgment text:\n got %q\nwant %q", got, want)
	}
}

func TestSingleCharterDraftAnswerIsIndexed(t *testing.T) {
	s := teamStore(t)
	sid := draftSession(t, s, "demo")
	res, err := s.db.Exec(`INSERT INTO charter_drafts(level,scope,attr,value,source,source_hash,drafted_by,created_at) VALUES('repo','demo','purpose','Demo','AGENTS.md','h',?,?)`, sid, s.now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	item, err := s.createInboxItem(s.db, protocol.InboxItem{
		Kind: protocol.AskKindDraft, SessionID: gatewayAsker, FromKind: protocol.AskFromGateway, Node: "demo", AskerName: "charter draft",
		SourceRef: "draft:" + itoa(id), AboutKind: protocol.AskAboutProject, AboutID: "demo",
		Text: "Draft repo purpose for demo", Options: []string{"accept", "reject"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInboxAs(item.ID, "reject", "cto"); err != nil {
		t.Fatal(err)
	}
	if got := oneAnswer(t, s, item.ID).Text; got != "Q: Draft repo purpose for demo / A: rejected: Draft repo purpose for demo" {
		t.Fatalf("judgment text: %q", got)
	}
}

func TestLoopAnswerIsIndexed(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	loopTicks(t, s)
	for i := 0; i < 4 && len(openLoopAsks(t, s)) == 0; i++ {
		idleSince(t, s, c, b, 0)
		runLoops(t, s, c)
	}
	asks := openLoopAsks(t, s)
	if len(asks) != 1 {
		t.Fatalf("loop asks: %+v", asks)
	}
	if _, err := s.AnswerInbox(asks[0].ID, protocol.LoopAnswerStop); err != nil {
		t.Fatal(err)
	}
	row := oneAnswer(t, s, asks[0].ID)
	if !strings.Contains(row.Text, "/ A: stopped the loop: ") {
		t.Fatalf("judgment text: %q", row.Text)
	}
}

// The owner overrules a rejection or lets it stand. The judgment says which,
// what was refused and why, and what request went through.
func TestOutcomeAnswerStoresWhatWasDecided(t *testing.T) {
	for _, tc := range []struct{ action, want string }{
		{"overrule", "overruled the rejection (outside charter): the request went through as asked: deploy research service X, Y, Z"},
		{"let_stand", "let the rejection stand (outside charter)"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			s, _ := newTestStore(t)
			a, b := twoSessions(t, s)
			node, _, _ := s.CreateNode("owner-host", "linux")
			ownerID, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: "owner"})
			req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "deploy research service X, Y, Z"})
			if err != nil {
				t.Fatal(err)
			}
			rep, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "no", ParentID: req.ID, ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "outside charter"}})
			if err != nil {
				t.Fatal(err)
			}
			item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "rejection", SessionID: a, MessageID: rep.ID, Text: "Request: deploy research service X, Y, Z"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.ResolveOutcome(rep.ID, ownerID, tc.action); err != nil {
				t.Fatal(err)
			}
			want := "Q: Request: deploy research service X, Y, Z / A: " + tc.want
			if got := oneAnswer(t, s, item.ID).Text; got != want {
				t.Fatalf("judgment text:\n got %q\nwant %q", got, want)
			}
		})
	}
}

// The owner types in the asking session: the ask closes as owner_replied and
// the judgment keeps his exact text. The frame carries the text.
func TestOwnerReplyInTheSessionStoresHisText(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	_, _ = s.UpsertSession(node, owner("test"))
	agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "c"})
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: agent, Node: "forge", Text: "Merge permission in forge sessions?"})
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s}
	raw, _ := json.Marshal(map[string]any{"session_id": agent, "prompt": true, "text": "yes, but contracts still go through tenebrae"})
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "session.owner_use", raw); err != nil {
		t.Fatal(err)
	}
	want := "Q: Merge permission in forge sessions? / A: yes, but contracts still go through tenebrae"
	if got := oneAnswer(t, s, item.ID).Text; got != want {
		t.Fatalf("judgment text:\n got %q\nwant %q", got, want)
	}
	var status, answer string
	if err := s.db.QueryRow(`SELECT status,answer FROM inbox_items WHERE id=?`, item.ID).Scan(&status, &answer); err != nil || status != "closed" || answer != protocol.AskReasonOwnerReplied {
		t.Fatalf("ask: %q %q %v", status, answer, err)
	}
}

// A frame without the text (a Pi session) still says what happened.
func TestOwnerReplyWithoutTextSaysSo(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: agent, Node: "forge", Text: "Which token?"})
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s}
	raw, _ := json.Marshal(map[string]any{"session_id": agent, "prompt": true})
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "session.owner_use", raw); err != nil {
		t.Fatal(err)
	}
	want := "Q: Which token? / A: replied in the asking session (the text was not captured)"
	if got := oneAnswer(t, s, item.ID).Text; got != want {
		t.Fatalf("judgment text: %q", got)
	}
}

// An ask that is answered again keeps one judgment row.
func TestAnsweringAnAskAgainUpdatesItsRow(t *testing.T) {
	s, c := newTestStore(t)
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: "s_x", Node: "proj", Text: "Ship it?"})
	if err != nil {
		t.Fatal(err)
	}
	item.Status = "answered"
	if err := s.recordOwnerAnswer(s.db, item, "no"); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(3600e9)
	if err := s.recordOwnerAnswer(s.db, item, "yes, after the tests"); err != nil {
		t.Fatal(err)
	}
	if got := oneAnswer(t, s, item.ID).Text; got != "Q: Ship it? / A: yes, after the tests" {
		t.Fatalf("judgment text: %q", got)
	}
	// The search index follows the update.
	if rows, err := s.FindJudgments(protocol.JudgmentsFindRequest{Query: "tests"}); err != nil || len(rows) != 1 {
		t.Fatalf("index: %+v %v", rows, err)
	}
	if rows, err := s.FindJudgments(protocol.JudgmentsFindRequest{Query: "no"}); err != nil || len(rows) != 0 {
		t.Fatalf("old text still indexed: %+v %v", rows, err)
	}
}

func insertAnsweredAsk(t *testing.T, s *Store, id, kind, status, text, answer string, at int64) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO inbox_items(id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,answer,answered_at)
		VALUES(?,?,'s_1','proj-alpha',?,'rec','m_1','c_1',?,?,?,?)`, id, kind, text, at-1e9, status, answer, at); err != nil {
		t.Fatal(err)
	}
}

// The backfill fills the asks that were answered while nothing wrote their
// judgments, puts a decision in plain words where an old row holds a bare
// code, and can run again without adding a row.
func TestBackfillFillsMissingRowsAndRunsAgainWithoutDuplicates(t *testing.T) {
	s, _ := newTestStore(t)
	insertAnsweredAsk(t, s, "i_missing", "question", "answered", "Which node?", "the Mac", 1_700_000_000e9)
	insertAnsweredAsk(t, s, "i_approved", "approval", "approved", "Deploy X?", "approve", 1_700_000_100e9)
	insertAnsweredAsk(t, s, "i_auto", "check", "closed", "Disk low", "activity_or_end", 1_700_000_200e9)
	// An older row that kept the bare code, and a mark on the same ask that
	// must not stop the answer from being indexed.
	insertAnsweredAsk(t, s, "i_oldcode", "approval", "approved", "Deploy Y?", "approve", 1_700_000_300e9)
	if _, err := s.db.Exec(`INSERT INTO standing_orders(id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref) VALUES('so_b_old','Q: Deploy Y? / A: approve',1700000300,'owner','[]',0,0,'answer','proj-alpha','i_oldcode')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO standing_orders(id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref) VALUES('so_mark','mark',1700000000,'owner','[]',0,0,'correction','chief','i_missing')`); err != nil {
		t.Fatal(err)
	}

	for run := 0; run < 2; run++ {
		if err := s.BackfillAnswerJudgments(); err != nil {
			t.Fatal(err)
		}
		if got := oneAnswer(t, s, "i_missing"); got.Text != "Q: Which node? / A: the Mac" || got.Area != "proj-alpha" || got.SavedAt != 1_700_000_000 {
			t.Fatalf("run %d i_missing: %+v", run, got)
		}
		if got := oneAnswer(t, s, "i_approved").Text; got != "Q: Deploy X? / A: approved: Deploy X?" {
			t.Fatalf("run %d i_approved: %q", run, got)
		}
		if got := oneAnswer(t, s, "i_oldcode"); got.Text != "Q: Deploy Y? / A: approved: Deploy Y?" || got.ID != "so_b_old" {
			t.Fatalf("run %d i_oldcode: %+v", run, got)
		}
		if rows := answerRows(t, s, "i_auto"); len(rows) != 0 {
			t.Fatalf("an automatic close is not an answer: %+v", rows)
		}
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM standing_orders WHERE kind='answer'`).Scan(&total); err != nil || total != 3 {
		t.Fatalf("answer rows after two runs: %d %v", total, err)
	}
}

// The gateway fills the gap at start: a store opened on a database that holds
// answered asks without judgments finds them afterwards.
func TestOpenStoreBackfillsMissingAnswers(t *testing.T) {
	path := seededStorePath(t, "gw.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	insertAnsweredAsk(t, s, "i_late", "question", "answered", "Keep the worktree?", "Keep it.", 1_700_000_000e9)
	s.Close()

	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := oneAnswer(t, s, "i_late").Text; got != "Q: Keep the worktree? / A: Keep it." {
		t.Fatalf("judgment text: %q", got)
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	oneAnswer(t, s, "i_late")
}

// The owner marks a sort on the Desk (n or m) through the gateway frame, and
// the judgment search with marks:true returns it with its ask.
func TestDeskMarkIsFoundByMarksSearch(t *testing.T) {
	s, _, chief, asker, node := chiefRig(t)
	ask := chiefAsk(t, s, asker, "Rotate the staging token?")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: ask, Result: protocol.AskSortClose, Reason: "routine"})
	g := &Gateway{store: s}
	raw, _ := json.Marshal(protocol.AskMarkRequest{Ask: ask, Mark: protocol.MarkDidntNeedMe, Why: "routine rotation"})
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "ask.mark", raw); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(protocol.JudgmentsFindRequest{Marks: true})
	res, err := g.handle(context.Background(), &nodeConn{id: node}, "judgment.find", raw)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	items := m["items"].([]protocol.StandingOrder)
	if len(items) != 1 || items[0].Ask != ask || items[0].Mark != protocol.MarkDidntNeedMe || items[0].Question != "Rotate the staging token?" {
		t.Fatalf("marks: %+v", items)
	}
	if m["marks_stored"] != 1 {
		t.Fatalf("marks_stored: %v", m["marks_stored"])
	}
}

// With no marks the answer says so, and says how many marks storage holds, so
// "none yet" is not mistaken for "not indexed".
func TestMarksSearchSaysHowManyMarksAreStored(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	g := &Gateway{store: s}
	raw, _ := json.Marshal(protocol.JudgmentsFindRequest{Marks: true})
	res, err := g.handle(context.Background(), &nodeConn{id: node}, "judgment.find", raw)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if len(m["items"].([]protocol.StandingOrder)) != 0 || m["marks_stored"] != 0 {
		t.Fatalf("result: %+v", m)
	}
	if note, _ := m["note"].(string); !strings.Contains(note, "0 owner marks") {
		t.Fatalf("note: %q", note)
	}
	// A search that is not about marks carries neither field.
	raw, _ = json.Marshal(protocol.JudgmentsFindRequest{})
	res, _ = g.handle(context.Background(), &nodeConn{id: node}, "judgment.find", raw)
	if _, has := res.(map[string]any)["marks_stored"]; has {
		t.Fatalf("plain search carries marks_stored: %+v", res)
	}
}

// The owner approves or refuses an app on the consent ask: the decision is
// kept, although the ask itself closes later as resolved.
func TestAppConsentAnswerIsIndexed(t *testing.T) {
	for _, answer := range []string{"approve", "refuse"} {
		t.Run(answer, func(t *testing.T) {
			s, _, h := oauthFixture(t)
			client := oauthRegister(t, h)
			code, _ := oauthPage(t, oauthAuthorize(h, client, nil))
			ask, err := s.OpenAppConsent(code, "bot", "Fixture")
			if err != nil {
				t.Fatal(err)
			}
			handled, err := s.answerAppConsent(ask.ID, answer, "", false)
			if err != nil || !handled {
				t.Fatalf("answer: %v %v", handled, err)
			}
			if got := oneAnswer(t, s, ask.ID).Text; !strings.Contains(got, " / A: "+map[string]string{"approve": "approved", "refuse": "refused"}[answer]+": ") {
				t.Fatalf("judgment text: %q", got)
			}
		})
	}
}

// A search term with a hyphen or a colon is text, not FTS5 syntax. The terms
// still join with AND, and an upper-case OR still means OR.
func TestJudgmentSearchTreatsHyphenAndColonAsText(t *testing.T) {
	s, _ := newTestStore(t)
	for _, text := range []string{
		"Add an ops scope to ingest-forge with the ops token: no hard deletes",
		"Keep the worktree until its commits are checked",
	} {
		if _, err := s.AddJudgment(protocol.JudgmentAddRequest{Kind: protocol.StandingKindRule, Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"ops scope ingest-forge token", 1},
		{"token: no", 1},
		{"ingest-forge", 1},
		{"ingest-forge OR worktree", 2},
		{"worktree commit*", 1},
		{`"ops scope"`, 1},
		{"ingest-forge worktree", 0},
		{`say "hi`, 0},
		{"-", 0},
	} {
		rows, err := s.FindJudgments(protocol.JudgmentsFindRequest{Query: tc.query})
		if err != nil {
			t.Fatalf("query %q: %v", tc.query, err)
		}
		if len(rows) != tc.want {
			t.Fatalf("query %q found %d rows, want %d: %+v", tc.query, len(rows), tc.want, rows)
		}
	}
	rows, err := s.SearchStandingOrders(protocol.StandingOrdersRequest{Query: "ops scope ingest-forge token"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("standing order search: %+v %v", rows, err)
	}
}
