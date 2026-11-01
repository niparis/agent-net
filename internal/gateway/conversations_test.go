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

	base := c.t
	insertConversationMessage(t, s, "m_c1a", "c_old", alice, bob, "old first line\nsecond line", base.Add(-3*time.Hour))
	insertConversationMessage(t, s, "m_c1b", "c_old", bob, alice, "old reply", base.Add(-2*time.Hour))
	insertConversationMessage(t, s, "m_c2", "c_mid", alice, bob, "middle message", base.Add(-time.Hour))
	insertConversationMessage(t, s, "m_c3a", "c_new", bob, alice, "newest message\nmore", base.Add(-2*time.Minute))
	insertConversationMessage(t, s, "m_c3b", "c_new", alice, bob, "newest reply", base.Add(-time.Minute))

	// A conversation between two sessions of another node is part of the
	// owner's gateway history: the list is not limited to this node.
	other, _, _ := s.CreateNode("h2", "linux")
	carol, _ := s.UpsertSession(other, protocol.SessionInfo{Harness: "pi", NativeID: "nc", Name: "carol"})
	dave, _ := s.UpsertSession(other, protocol.SessionInfo{Harness: "pi", NativeID: "nd", Name: "dave"})
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
	if page.Conversations[0].ID != "c_other" || page.Conversations[1].ID != "c_new" {
		t.Fatalf("first page order = %s, %s", page.Conversations[0].ID, page.Conversations[1].ID)
	}
	if page.Next == "" {
		t.Fatal("first page did not give a cursor")
	}
	first := page.Conversations[1]
	if first.From != alice || first.To != bob || first.FromName != "alice" || first.ToName != "bob" {
		t.Errorf("sides = %s/%s (%s/%s)", first.From, first.To, first.FromName, first.ToName)
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
	if len(next.Conversations) != 2 || next.Conversations[0].ID != "c_mid" || next.Conversations[1].ID != "c_old" {
		t.Fatalf("second page = %+v", next.Conversations)
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
	if normal.ID != "c_normal" || normal.Count != 1 || normal.LastLine != "normal agent message" ||
		normal.From != alice || normal.To != bob || normal.FromName != "alice" || normal.ToName != "bob" {
		t.Fatalf("normal conversation changed: %+v", normal)
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
	if !seen["c_plain"] {
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
	if elapsed > 2*time.Second {
		t.Errorf("first page is too slow: %s", elapsed)
	}
}
