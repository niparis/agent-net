package gateway

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// conversationFixture makes one node with two sessions and returns their IDs.
func conversationFixture(t *testing.T, s *Store) (node, alice, bob string) {
	t.Helper()
	var err error
	node, _, err = s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	alice, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "na", Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "nb", Name: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	return node, alice, bob
}

// insertConversationMessage writes one stored message without the send rules
// (rate and conversation budget), so a test can set up an old history.
func insertConversationMessage(t *testing.T, s *Store, id, conv, from, to, body string, at time.Time) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO messages
		(id, conversation_id, parent_id, sender, recipient, type, body, hop, idem_key, created_at, expires_at, state)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, conv, "", from, to, protocol.TypeRequest, body, 0, "k-"+id,
		at.UnixNano(), at.Add(protocol.DefaultTTL).UnixNano(), protocol.StateStored)
	if err != nil {
		t.Fatal(err)
	}
}

func TestConversationsPageCursorTotalAndOrder(t *testing.T) {
	s, c := newTestStore(t)
	_, alice, bob := conversationFixture(t, s)
	// Two sessions of another node are part of the owner's gateway history:
	// the list is not limited to this node.
	other, _, _ := s.CreateNode("h2", "linux")
	carol, _ := s.UpsertSession(other, protocol.SessionInfo{Harness: "pi", NativeID: "nc", Name: "carol"})
	dave, _ := s.UpsertSession(other, protocol.SessionInfo{Harness: "pi", NativeID: "nd", Name: "dave"})

	base := c.t
	insertConversationMessage(t, s, "m_c1a", "c_old", alice, bob, "old first line\nsecond line", base.Add(-3*time.Hour))
	insertConversationMessage(t, s, "m_c1b", "c_old", bob, alice, "old reply", base.Add(-2*time.Hour))
	insertConversationMessage(t, s, "m_c2", "c_mid", alice, carol, "middle message", base.Add(-time.Hour))
	insertConversationMessage(t, s, "m_c3a", "c_new", bob, dave, "newest message\nmore", base.Add(-2*time.Minute))
	insertConversationMessage(t, s, "m_c3b", "c_new", dave, bob, "newest reply", base.Add(-time.Minute))
	insertConversationMessage(t, s, "m_other", "c_other", carol, dave, "elsewhere", base)

	page, err := s.ConversationsPage(protocol.QueryConversations{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 4 {
		t.Fatalf("total = %d, want 4", page.Total)
	}
	if len(page.Conversations) != 2 {
		t.Fatalf("first page has %d rows, want 2", len(page.Conversations))
	}
	if page.Conversations[0].From != carol || page.Conversations[0].To != dave {
		t.Fatalf("first page order starts with %s/%s", page.Conversations[0].From, page.Conversations[0].To)
	}
	if page.Conversations[1].From != dave || page.Conversations[1].To != bob {
		t.Fatalf("first page second row = %s/%s", page.Conversations[1].From, page.Conversations[1].To)
	}
	if page.Next == "" {
		t.Fatal("first page did not give a cursor")
	}
	first := page.Conversations[1]
	if first.FromName != "dave" || first.ToName != "bob" {
		t.Errorf("names = %s/%s", first.FromName, first.ToName)
	}
	if first.Count != 2 {
		t.Errorf("count = %d, want 2", first.Count)
	}
	if first.LastLine != "newest reply" {
		t.Errorf("last line = %q", first.LastLine)
	}
	if first.State != protocol.StateStored {
		t.Errorf("state = %q", first.State)
	}
	if !first.At.Equal(base.Add(-time.Minute)) {
		t.Errorf("at = %v", first.At)
	}
	if first.LastMessageID != "m_c3b" {
		t.Errorf("last message = %q", first.LastMessageID)
	}

	next, err := s.ConversationsPage(protocol.QueryConversations{Cursor: page.Next, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if next.Total != 4 || next.Next != "" {
		t.Fatalf("second page total=%d next=%q", next.Total, next.Next)
	}
	if len(next.Conversations) != 2 {
		t.Fatalf("second page = %+v", next.Conversations)
	}
	if next.Conversations[0].From != alice || next.Conversations[0].To != carol {
		t.Fatalf("second page first row = %s/%s", next.Conversations[0].From, next.Conversations[0].To)
	}
	if next.Conversations[1].LastLine != "old reply" {
		t.Errorf("last line = %q", next.Conversations[1].LastLine)
	}
}

// endedWorker makes a worker session whose gateway record is then deleted, as
// the cleanup does a few hours after the start. It returns the session ID.
func endedWorker(t *testing.T, s *Store, node, owner, native, task string) string {
	t.Helper()
	worker, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: native,
		Name: "shop worker 8", Worker: &protocol.WorkerInfo{ID: "w_" + native, Owner: owner, Task: task}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, worker); err != nil {
		t.Fatal(err)
	}
	return worker
}

func TestConversationsPageReportsThreadFlags(t *testing.T) {
	s, c := newTestStore(t)
	_, alice, bob := conversationFixture(t, s)
	base := c.t
	// One busy hour and three quiet hours: peak 25, median 1.
	insertConversationMessage(t, s, "m_burst0", "c_burst", alice, bob, "blocked on the deploy", base.Add(-3*time.Hour))
	for i := 1; i <= 24; i++ {
		insertConversationMessage(t, s, fmt.Sprintf("m_burst%d", i), "c_burst", alice, bob, "line", base.Add(-3*time.Hour+time.Duration(i)*time.Minute))
	}
	insertConversationMessage(t, s, "m_quiet1", "c_burst", alice, bob, "quiet", base.Add(-5*time.Hour))
	insertConversationMessage(t, s, "m_quiet2", "c_burst", alice, bob, "quiet", base.Add(-6*time.Hour))
	if _, err := s.db.Exec(`UPDATE messages SET owner_approved=1 WHERE id='m_burst0'`); err != nil {
		t.Fatal(err)
	}
	insertConversationMessage(t, s, "m_sys", "c_sys", "agent-net", alice, "[agent-net] check: is your task done?", base.Add(-time.Minute))

	page, err := s.ConversationsPage(protocol.QueryConversations{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var burst, sys *protocol.ConversationView
	for i := range page.Conversations {
		if page.Conversations[i].System {
			sys = &page.Conversations[i]
		} else {
			burst = &page.Conversations[i]
		}
	}
	if burst == nil || sys == nil {
		t.Fatalf("rows: %+v", page.Conversations)
	}
	if burst.FromHarness != "claude" || burst.ToHarness != "pi" {
		t.Errorf("harness = %q/%q", burst.FromHarness, burst.ToHarness)
	}
	if !burst.OwnerNamed {
		t.Error("owner approval was not reported")
	}
	if !burst.Blocked {
		t.Error("a blocked action was not reported")
	}
	if burst.PeakHour != 25 || burst.MedianHour != 1 {
		t.Errorf("spread = %d/%d, want 25/1", burst.PeakHour, burst.MedianHour)
	}
	if !sys.System || !strings.HasPrefix(sys.SystemLine, "[agent-net] check:") || sys.SystemTo != alice {
		t.Errorf("system row = %+v", sys)
	}
}

func TestConversationsPageGroupsAnEndedWorkerAndNamesItFromTheTask(t *testing.T) {
	s, c := newTestStore(t)
	node, alice, bob := conversationFixture(t, s)
	base := c.t
	worker := endedWorker(t, s, node, alice, "nw", "Polish the sessions panel")

	taskBody := protocol.WorkerTaskBody("Polish the sessions panel\nRun the checks.", "/tmp/wt", "agent-net/w_8")
	insertConversationMessage(t, s, "m_task", "c_task", alice, worker, taskBody, base.Add(-10*time.Minute))
	started := protocol.WorkerStartedText(worker, "opencode", "m1", "shop", "mac", time.Minute)
	insertConversationMessage(t, s, "m_started", "c_started", worker, alice, started, base.Add(-9*time.Minute))
	insertConversationMessage(t, s, "m_done", "c_done", worker, alice,
		"[agent-net worker notice]\nstatus: done\nrun time: 1m\n", base.Add(-time.Minute))
	// A lifecycle notice after the hand-back must not move the row again.
	insertConversationMessage(t, s, "m_late_started", "c_late_started", worker, alice, started, base.Add(-30*time.Second))
	insertConversationMessage(t, s, "m_normal", "c_normal", alice, bob, "normal agent message", base.Add(-5*time.Minute))

	page, err := s.ConversationsPage(protocol.QueryConversations{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Conversations) != 2 {
		t.Fatalf("total=%d rows=%d, want the worker group and one normal conversation", page.Total, len(page.Conversations))
	}
	got := page.Conversations[0]
	if got.ID != "worker:"+worker || got.WorkerSession != worker || got.WorkerOwner != alice {
		t.Fatalf("worker row = %+v", got)
	}
	if got.From != alice || got.To != worker || got.FromName != "alice" {
		t.Fatalf("worker sides = %s/%s (%s)", got.From, got.To, got.FromName)
	}
	if got.Task != "Polish the sessions panel" {
		t.Errorf("worker task = %q", got.Task)
	}
	if got.Count != 2 {
		t.Errorf("worker count = %d, want the task and the hand-back only", got.Count)
	}
	if got.LastMessageID != "m_done" || !got.At.Equal(base.Add(-time.Minute)) {
		t.Errorf("worker row moved to %s at %v, want the hand-back", got.LastMessageID, got.At)
	}
	normal := page.Conversations[1]
	if !strings.HasPrefix(normal.ID, "pair:") || normal.Count != 1 || normal.LastLine != "normal agent message" ||
		normal.From != alice || normal.To != bob || normal.FromName != "alice" || normal.ToName != "bob" {
		t.Fatalf("normal conversation changed: %+v", normal)
	}
}

func TestConversationsPageGroupsAPeerPairAndItsBurst(t *testing.T) {
	s, c := newTestStore(t)
	_, alice, bob := conversationFixture(t, s)
	base := c.t
	// 36 conversations between the same two parties. 22 messages fall in one
	// hour and 14 fall in 14 other hours, so the pair is one row with a peak of
	// 22 and a median of 1.
	for i := 0; i < 22; i++ {
		insertConversationMessage(t, s, fmt.Sprintf("m_busy_%d", i), fmt.Sprintf("c_busy_%d", i),
			alice, bob, "busy", base.Add(-time.Hour+time.Duration(i)*time.Minute))
	}
	for i := 0; i < 14; i++ {
		insertConversationMessage(t, s, fmt.Sprintf("m_quiet_%d", i), fmt.Sprintf("c_quiet_%d", i),
			bob, alice, "quiet", base.Add(-time.Duration(i+2)*time.Hour))
	}
	page, err := s.ConversationsPage(protocol.QueryConversations{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Conversations) != 1 {
		t.Fatalf("total=%d rows=%d, want one pair row", page.Total, len(page.Conversations))
	}
	row := page.Conversations[0]
	if !strings.HasPrefix(row.ID, "pair:") {
		t.Fatalf("row id = %q, want a pair id", row.ID)
	}
	if row.Count != 36 {
		t.Errorf("count = %d, want 36", row.Count)
	}
	if row.PeakHour != 22 || row.MedianHour != 1 {
		t.Errorf("spread = %d/%d, want 22/1", row.PeakHour, row.MedianHour)
	}
	msgs, err := s.ConversationMessages(row.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 36 {
		t.Fatalf("opening the pair returned %d messages, want 36", len(msgs))
	}
}

func TestConversationsPageHidesLifecycleOnlyWorkerConversations(t *testing.T) {
	s, c := newTestStore(t)
	node, alice, _ := conversationFixture(t, s)
	base := c.t
	worker := endedWorker(t, s, node, alice, "nw", "")
	started := protocol.WorkerStartedText(worker, "opencode", "m1", "shop", "mac", time.Minute)
	insertConversationMessage(t, s, "m_started", "c_started", worker, alice, started, base.Add(-2*time.Minute))
	insertConversationMessage(t, s, "m_queued", "c_queued", worker, alice,
		"[agent-net worker notice]\nstatus: queued\nnote: waiting for room\n", base.Add(-time.Minute))
	insertConversationMessage(t, s, "m_not_started", "c_not_started", alice, alice,
		protocol.WorkerNotStartedText("shop", "opencode", "no room"), base.Add(-time.Minute))

	page, err := s.ConversationsPage(protocol.QueryConversations{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Conversations) != 0 {
		t.Fatalf("lifecycle notices made rows: total=%d %+v", page.Total, page.Conversations)
	}
}

func TestConversationsPageGroupsWorkerPagesAndTotals(t *testing.T) {
	s, c := newTestStore(t)
	node, alice, bob := conversationFixture(t, s)
	base := c.t
	var want []string
	for i := 0; i < 5; i++ {
		worker := endedWorker(t, s, node, alice, fmt.Sprintf("nw%d", i), fmt.Sprintf("Task %d", i))
		at := base.Add(-time.Duration(10-i*2) * time.Minute)
		insertConversationMessage(t, s, fmt.Sprintf("m_task_%d", i), fmt.Sprintf("c_task_%d", i), alice, worker,
			protocol.WorkerTaskBody(fmt.Sprintf("Task %d", i), "", ""), at)
		insertConversationMessage(t, s, fmt.Sprintf("m_started_%d", i), fmt.Sprintf("c_started_%d", i), worker, alice,
			protocol.WorkerStartedText(worker, "opencode", "m1", "shop", "mac", 0), at.Add(time.Minute))
		insertConversationMessage(t, s, fmt.Sprintf("m_done_%d", i), fmt.Sprintf("c_done_%d", i), worker, alice,
			"[agent-net worker notice]\nstatus: done\n", at.Add(2*time.Minute))
		want = append(want, "worker:"+worker)
	}
	insertConversationMessage(t, s, "m_plain", "c_plain", alice, bob, "a normal message", base.Add(-time.Minute))

	seen := map[string]bool{}
	normalSeen := false
	var cursor string
	for {
		page, err := s.ConversationsPage(protocol.QueryConversations{Cursor: cursor, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 6 {
			t.Fatalf("total = %d, want 5 worker groups and one normal conversation", page.Total)
		}
		if len(page.Conversations) > 2 {
			t.Fatalf("page has %d rows", len(page.Conversations))
		}
		for _, v := range page.Conversations {
			if seen[v.ID] {
				t.Fatalf("row %s appeared twice", v.ID)
			}
			seen[v.ID] = true
			if strings.HasPrefix(v.ID, "worker:") && v.Count != 2 {
				t.Errorf("row %s count = %d, want task and hand-back only", v.ID, v.Count)
			}
			if !strings.HasPrefix(v.ID, "worker:") {
				normalSeen = true
				if v.Count != 1 {
					t.Errorf("normal row count = %d, want 1", v.Count)
				}
			}
		}
		cursor = page.Next
		if cursor == "" {
			break
		}
	}
	if len(seen) != 6 {
		t.Fatalf("walked %d rows, want 6", len(seen))
	}
	for _, id := range want {
		if !seen[id] {
			t.Errorf("worker group %s missing", id)
		}
	}
	if !normalSeen {
		t.Error("normal conversation missing")
	}
}

func TestConversationMessagesOpensAGroupedWorker(t *testing.T) {
	s, c := newTestStore(t)
	node, alice, _ := conversationFixture(t, s)
	base := c.t
	worker := endedWorker(t, s, node, alice, "nw", "Polish the panels")
	insertConversationMessage(t, s, "m_task", "c_task", alice, worker,
		protocol.WorkerTaskBody("Polish the panels", "", ""), base.Add(-2*time.Minute))
	insertConversationMessage(t, s, "m_started", "c_started", worker, alice,
		protocol.WorkerStartedText(worker, "opencode", "m1", "shop", "mac", 0), base.Add(-time.Minute))

	msgs, err := s.ConversationMessages("worker:"+worker, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].ID != "m_started" || msgs[1].ID != "m_task" {
		t.Fatalf("worker conversation = %+v", msgs)
	}
	if msgs[0].Body == "" || !strings.Contains(msgs[0].Body, "status: started") {
		t.Fatalf("the lifecycle notice is not visible when the worker is opened: %+v", msgs[0])
	}
}

func TestConversationMessagesCoverEveryNodeForTheOwner(t *testing.T) {
	s, c := newTestStore(t)
	other, _, _ := s.CreateNode("h2", "linux")
	carol, _ := s.UpsertSession(other, protocol.SessionInfo{Harness: "pi", NativeID: "nc", Name: "carol"})
	dave, _ := s.UpsertSession(other, protocol.SessionInfo{Harness: "pi", NativeID: "nd", Name: "dave"})
	base := c.t
	insertConversationMessage(t, s, "m_r1", "c_remote", carol, dave, "remote request", base.Add(-2*time.Minute))
	insertConversationMessage(t, s, "m_r2", "c_remote", dave, carol, "remote reply", base.Add(-time.Minute))

	msgs, err := s.ConversationMessages("c_remote", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].ID != "m_r2" || msgs[1].ID != "m_r1" {
		t.Fatalf("messages = %+v", msgs)
	}
	if msgs[0].Body != "remote reply" || msgs[0].From != dave || msgs[0].To != carol {
		t.Fatalf("newest message = %+v", msgs[0])
	}
}

func TestConversationsPageGroupsTenThousandWorkerMessagesFast(t *testing.T) {
	s, c := newTestStore(t)
	_, alice, bob := conversationFixture(t, s)

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	const workers, convs = 100, 100
	for w := 0; w < workers; w++ {
		worker := fmt.Sprintf("s_worker_%03d", w)
		for n, body := range []string{
			protocol.WorkerTaskBody(fmt.Sprintf("Task %d", w), "", ""),
			"[agent-net worker notice]\nstatus: started\nworker: " + worker + "\n",
			"[agent-net worker notice]\nstatus: done\nrun time: 1m\n",
		} {
			id := fmt.Sprintf("m_w%d_%d", w, n)
			at := c.t.Add(-time.Duration(w*3+n) * time.Minute)
			from, to := alice, worker
			if n > 0 {
				from, to = worker, alice
			}
			if _, err := tx.Exec(`INSERT INTO messages
				(id, conversation_id, parent_id, sender, recipient, type, body, hop, idem_key, created_at, expires_at, state)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
				id, fmt.Sprintf("c_w%d_%d", w, n), "", from, to, protocol.TypeRequest,
				body, 0, "k-"+id, at.UnixNano(), at.Add(protocol.DefaultTTL).UnixNano(), protocol.StateStored); err != nil {
				t.Fatal(err)
			}
		}
	}
	for conv := 0; conv < convs; conv++ {
		for msg := 0; msg < 97; msg++ {
			id := fmt.Sprintf("m_%d_%d", conv, msg)
			at := c.t.Add(-time.Duration(conv*97+msg) * time.Minute)
			if _, err := tx.Exec(`INSERT INTO messages
				(id, conversation_id, parent_id, sender, recipient, type, body, hop, idem_key, created_at, expires_at, state)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
				id, fmt.Sprintf("c_%d", conv), "", alice, bob, protocol.TypeRequest,
				"line for "+id, 0, "k-"+id, at.UnixNano(), at.Add(protocol.DefaultTTL).UnixNano(), protocol.StateStored); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	page, err := s.ConversationsPage(protocol.QueryConversations{Limit: 50})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("grouped conversation page over 10000 messages took %s", elapsed)
	if page.Total != workers+convs || len(page.Conversations) != 50 || page.Next == "" {
		t.Fatalf("page total=%d rows=%d next=%q", page.Total, len(page.Conversations), page.Next)
	}
	if page.Conversations[0].Count != 2 {
		t.Errorf("worker row count = %d, want 2", page.Conversations[0].Count)
	}
	// About 40 ms without the race detector and up to 4 s with it on a busy
	// CI runner; the unindexed query this guards against took 20 s or more.
	if elapsed > 10*time.Second {
		t.Errorf("first grouped page is too slow: %s", elapsed)
	}
}

func TestConversationsPageKeepsTenThousandMessagesFast(t *testing.T) {
	s, c := newTestStore(t)
	_, alice, bob := conversationFixture(t, s)

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for conv := 0; conv < 500; conv++ {
		for msg := 0; msg < 20; msg++ {
			id := fmt.Sprintf("m_%d_%d", conv, msg)
			at := c.t.Add(-time.Duration(conv*20+msg) * time.Minute)
			if _, err := tx.Exec(`INSERT INTO messages
				(id, conversation_id, parent_id, sender, recipient, type, body, hop, idem_key, created_at, expires_at, state)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
				id, fmt.Sprintf("c_%d", conv), "", alice, bob, protocol.TypeRequest,
				"line for "+id, 0, "k-"+id, at.UnixNano(), at.Add(protocol.DefaultTTL).UnixNano(), protocol.StateStored); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	page, err := s.ConversationsPage(protocol.QueryConversations{Limit: 50})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("first conversation page over 10000 messages took %s", elapsed)
	if page.Total != 500 || len(page.Conversations) != 50 || page.Next == "" {
		t.Fatalf("page total=%d rows=%d next=%q", page.Total, len(page.Conversations), page.Next)
	}
	if elapsed > 10*time.Second {
		t.Errorf("first page is too slow: %s", elapsed)
	}
}
