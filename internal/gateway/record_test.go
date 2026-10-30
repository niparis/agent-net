package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// logCapture collects the log lines a store writes.
type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func (l *logCapture) add(line string) {
	l.mu.Lock()
	l.lines = append(l.lines, line)
	l.mu.Unlock()
}

func (l *logCapture) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// with returns the lines that hold every given text.
func (l *logCapture) with(parts ...string) []string {
	var out []string
	for _, line := range l.all() {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(line, p)
		}
		if ok {
			out = append(out, line)
		}
	}
	return out
}

func newLoggedStore(t *testing.T) (*Store, *clock, *logCapture) {
	t.Helper()
	s, c := newTestStore(t)
	lc := &logCapture{}
	s.logf = func(event string, kv ...any) { lc.add(formatLine(c.now(), event, kv...)) }
	return s, c, lc
}

// ---- log format ----

func TestFormatLineQuotesAndDropsEmpty(t *testing.T) {
	ts := time.Date(2026, 10, 29, 12, 30, 45, 123e6, time.UTC)
	got := formatLine(ts, "msg.stored", "msg", "m_1", "empty", "", "name", "my agent", "n", 7,
		"evil", "x\nts=1 event=fake", "eq", "a=b")
	want := `ts=2026-10-29T12:30:45.123Z event=msg.stored msg=m_1 name="my agent" n=7 evil="x\nts=1 event=fake" eq="a=b"`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if strings.Contains(got, "\n") {
		t.Fatal("a log line must be one line")
	}
}

// ---- message log lines ----

func TestMessageLogLines(t *testing.T) {
	s, c, lc := newLoggedStore(t)
	a, b := twoSessions(t, s)
	if _, err := s.Announce(mustNode(t, s, a), protocol.ProfileAnnounce{SessionID: a, Name: "alice the agent"}); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Second)
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "TOP-SECRET-BODY-TEXT", IdempotencyKey: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	stored := lc.with("event=msg.stored")
	if len(stored) != 1 {
		t.Fatalf("stored lines: %v", stored)
	}
	line := stored[0]
	for _, want := range []string{
		"ts=2026-10-28T12:00:01.000Z", "msg=" + m.ID, "conv=" + m.ConversationID, "type=request",
		"from=" + a, `from_name="alice the agent"`, "from_harness=claude", "from_host=h1",
		"to=" + b, "to_name=bob", "to_harness=pi", "to_host=h2", "bytes=20",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("stored line lacks %q:\n%s", want, line)
		}
	}
	// The same idempotency key stores nothing and logs nothing new.
	if _, created, _ := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "TOP-SECRET-BODY-TEXT", IdempotencyKey: "k1"}); created {
		t.Fatal("repeat was created")
	}
	if n := len(lc.with("event=msg.stored")); n != 1 {
		t.Fatalf("repeat logged again: %d lines", n)
	}

	if err := s.MarkDispatched(m.ID); err != nil {
		t.Fatal(err)
	}
	s.MarkDispatched(m.ID) // second call changes nothing
	if err := s.ApplyDelivery(m.ID, protocol.StateDelivered, "ok"); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"msg.dispatched", "msg.delivered"} {
		ls := lc.with("event="+ev, "msg="+m.ID, "conv="+m.ConversationID, "to="+b, "to_host=h2")
		if len(ls) != 1 {
			t.Fatalf("%s lines: %v", ev, ls)
		}
	}
	if d := lc.with("event=msg.delivered"); !strings.Contains(d[0], "detail=ok") {
		t.Fatalf("delivered line lacks the detail: %s", d[0])
	}

	// A failure and an expiry.
	m2, _, _ := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "second", IdempotencyKey: "k2"})
	s.ApplyDelivery(m2.ID, protocol.StateFailed, "no such local session")
	if ls := lc.with("event=msg.failed", "msg="+m2.ID, `detail="no such local session"`); len(ls) != 1 {
		t.Fatalf("failed lines: %v", lc.all())
	}
	m3, _, _ := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "third", IdempotencyKey: "k3"})
	c.t = c.t.Add(protocol.DefaultTTL + time.Second)
	if n, _ := s.Expire(); n != 1 {
		t.Fatalf("expired %d", n)
	}
	if ls := lc.with("event=msg.expired", "msg="+m3.ID); len(ls) != 1 {
		t.Fatalf("expired lines: %v", lc.all())
	}

	// A reply says so.
	rep, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "r", ParentID: m.ID, IdempotencyKey: "k4"})
	if err != nil {
		t.Fatal(err)
	}
	if ls := lc.with("event=msg.stored", "msg="+rep.ID, "type=reply", "conv="+m.ConversationID); len(ls) != 1 {
		t.Fatalf("reply lines: %v", lc.all())
	}

	for _, line := range lc.all() {
		if strings.Contains(line, "TOP-SECRET-BODY-TEXT") || strings.Contains(line, "second") {
			t.Fatalf("a log line holds a message body: %s", line)
		}
		if strings.Contains(line, "\n") {
			t.Fatalf("multi-line log entry: %q", line)
		}
	}
	// The events table keeps no body either.
	var bodies int
	s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE detail LIKE '%TOP-SECRET%'`).Scan(&bodies)
	if bodies != 0 {
		t.Fatal("an event holds a message body")
	}
}

func TestSessionNewLoggedOnce(t *testing.T) {
	s, _, lc := newLoggedStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	info := protocol.SessionInfo{Harness: "opencode", NativeID: "n1", Name: "oc", Cwd: "/w/proj", PID: 42,
		Repo: protocol.RepoInfo{Root: "/w/proj", Project: "proj", Remote: "https://example.com/o/proj.git", Branch: "main"}}
	id, err := s.UpsertSession(node, info)
	if err != nil {
		t.Fatal(err)
	}
	lines := lc.with("event=session.new")
	if len(lines) != 1 {
		t.Fatalf("lines: %v", lc.all())
	}
	for _, want := range []string{"session=" + id, "node=" + node, "host=box", "harness=opencode", "name=oc", "cwd=/w/proj",
		"project=proj", "git_root=/w/proj", "remote=https://example.com/o/proj.git", "branch=main"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("line lacks %q: %s", want, lines[0])
		}
	}
	info.Repo.Branch = "other"
	if id2, _ := s.UpsertSession(node, info); id2 != id {
		t.Fatal("session id changed")
	}
	if _, err := s.SyncSessions(node, []protocol.SessionInfo{info}); err != nil {
		t.Fatal(err)
	}
	if n := len(lc.with("event=session.new")); n != 1 {
		t.Fatalf("known session logged again: %d", n)
	}
	// A new session in a sync is logged too, after the commit.
	if _, err := s.SyncSessions(node, []protocol.SessionInfo{info, {Harness: "claude", NativeID: "n2", Cwd: "/w/other"}}); err != nil {
		t.Fatal(err)
	}
	if n := len(lc.with("event=session.new", "harness=claude")); n != 1 {
		t.Fatalf("sync did not log the new session: %v", lc.all())
	}
	// And the events table has the rows.
	evs, _ := s.QueryEvents(EventFilter{Kind: "session.new"})
	if len(evs) != 2 || !strings.Contains(evs[0].Detail, "project=proj") {
		t.Fatalf("events: %+v", evs)
	}
	// The stored repo info comes back with the agent.
	a, ok, _ := s.Agent(id)
	if !ok || a.Repo != (protocol.RepoInfo{Root: "/w/proj", Project: "proj", Remote: "https://example.com/o/proj.git", Branch: "other"}) {
		t.Fatalf("agent repo: %+v", a.Repo)
	}
}

// ---- projects ----

func TestProjects(t *testing.T) {
	s, c := newTestStore(t)
	n1, _, _ := s.CreateNode("mac", "darwin")
	n2, _, _ := s.CreateNode("linux-box", "linux")
	repo := protocol.RepoInfo{Root: "/src/shop", Project: "shop", Remote: "https://example.com/shop.git", Branch: "main"}
	s.UpsertSession(n1, protocol.SessionInfo{Harness: "claude", NativeID: "a", Cwd: "/src/shop/web", Repo: repo})
	c.t = c.t.Add(time.Hour)
	s.UpsertSession(n1, protocol.SessionInfo{Harness: "opencode", NativeID: "b", Cwd: "/src/shop", Repo: repo})
	s.UpsertSession(n2, protocol.SessionInfo{Harness: "pi", NativeID: "c", Cwd: "/home/u/shop",
		Repo: protocol.RepoInfo{Root: "/home/u/shop", Project: "shop"}})
	// No repository info: named after the folder. A session without a folder is skipped.
	s.UpsertSession(n2, protocol.SessionInfo{Harness: "claude", NativeID: "d", Cwd: "/tmp/scratch"})
	s.UpsertSession(n2, protocol.SessionInfo{Harness: "claude", NativeID: "e"})
	s.SyncSessions(n1, []protocol.SessionInfo{{Harness: "opencode", NativeID: "b", Cwd: "/src/shop", Repo: repo}}) // a goes offline

	ps, err := s.Projects("")
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		name, host, path, remote string
		harnesses                []string
		sessions, online         int
	}
	var got []row
	for _, p := range ps {
		got = append(got, row{p.Name, p.Hostname, p.Path, p.Remote, p.Harnesses, p.Sessions, p.Online})
	}
	want := []row{
		{"scratch", "linux-box", "/tmp/scratch", "", []string{"claude"}, 1, 1},
		{"shop", "linux-box", "/home/u/shop", "", []string{"pi"}, 1, 1},
		{"shop", "mac", "/src/shop", "https://example.com/shop.git", []string{"claude", "opencode"}, 2, 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if ps[2].LastSeen != c.t.Unix() {
		t.Fatalf("last seen %d, want %d", ps[2].LastSeen, c.t.Unix())
	}
	one, _ := s.Projects("SHOP")
	if len(one) != 2 {
		t.Fatalf("name filter (case-insensitive): %+v", one)
	}
}

// A message whose write to the node failed goes back to stored, so the next
// dispatch sends it again. A delivered message is left alone.
func TestUnmarkDispatched(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	m, _, _ := send(s, a, b, "k", "")
	s.MarkDispatched(m.ID)
	s.UnmarkDispatched(m.ID)
	if got, _ := s.Pending(mustNode(t, s, b), false); len(got) != 1 {
		t.Fatalf("pending after unmark: %d, want 1", len(got))
	}
	s.MarkDispatched(m.ID)
	s.ApplyDelivery(m.ID, protocol.StateDelivered, "")
	s.UnmarkDispatched(m.ID)
	if got, _ := s.Pending(mustNode(t, s, b), true); len(got) != 0 {
		t.Fatalf("a delivered message is pending again: %d", len(got))
	}
}

// ---- events ----

func TestQueryEventsFilters(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	nodeA, nodeB := mustNode(t, s, a), mustNode(t, s, b)
	s.Event("node.connected", nodeA, "", "", "127.0.0.1")
	c.t = c.t.Add(time.Hour)
	m, _, _ := send(s, a, b, "k", "")
	s.MarkDispatched(m.ID)
	s.ApplyDelivery(m.ID, protocol.StateDelivered, "")
	c.t = c.t.Add(time.Hour)
	s.Event("node.connected", nodeB, "", "", "127.0.0.2")

	kinds := func(f EventFilter) string {
		evs, err := s.QueryEvents(f)
		if err != nil {
			t.Fatal(err)
		}
		var k []string
		for _, e := range evs {
			k = append(k, e.Kind)
		}
		return strings.Join(k, ",")
	}
	if got := kinds(EventFilter{Kind: "msg"}); got != "msg.stored,msg.dispatched,msg.delivered" {
		t.Errorf("kind prefix: %s", got)
	}
	if got := kinds(EventFilter{Kind: "msg.stored"}); got != "msg.stored" {
		t.Errorf("kind exact: %s", got)
	}
	if got := kinds(EventFilter{Kind: "ms"}); got != "" {
		t.Errorf("a partial word must not match: %s", got)
	}
	if got := kinds(EventFilter{Session: a, Kind: "msg"}); got != "msg.stored,msg.dispatched,msg.delivered" {
		t.Errorf("session (sender): %s", got)
	}
	if got := kinds(EventFilter{Session: b, Kind: "msg"}); got != "msg.stored,msg.dispatched,msg.delivered" {
		t.Errorf("session (recipient): %s", got)
	}
	if got := kinds(EventFilter{Session: "s_nobody"}); got != "" {
		t.Errorf("unknown session: %s", got)
	}
	// Delivery steps name the recipient session, the stored event the sender.
	steps, _ := s.QueryEvents(EventFilter{Kind: "msg"})
	if steps[0].SessionID != a || steps[1].SessionID != b || steps[2].SessionID != b {
		t.Errorf("session ids of the message events: %+v", steps)
	}
	// By node ID and by host name; the node's messages count.
	if got := kinds(EventFilter{Node: nodeA, Kind: "node.connected"}); got != "node.connected" {
		t.Errorf("node id: %s", got)
	}
	if got := kinds(EventFilter{Node: "h2"}); !strings.Contains(got, "msg.delivered") || !strings.Contains(got, "node.connected") {
		t.Errorf("node host name: %s", got)
	}
	if got := kinds(EventFilter{Node: "nope"}); got != "" {
		t.Errorf("unknown node: %s", got)
	}
	// Time and limit.
	if got := kinds(EventFilter{Since: c.t.Add(-30 * time.Minute)}); got != "node.connected" {
		t.Errorf("since: %s", got)
	}
	all, _ := s.QueryEvents(EventFilter{})
	last2, _ := s.QueryEvents(EventFilter{Limit: 2})
	if len(last2) != 2 || last2[1].ID != all[len(all)-1].ID || last2[0].ID >= last2[1].ID {
		t.Errorf("limit keeps the newest two, oldest first: %+v", last2)
	}
	after, _ := s.QueryEvents(EventFilter{AfterID: all[len(all)-3].ID})
	if len(after) != 2 || after[0].ID != all[len(all)-2].ID {
		t.Errorf("after_id: %+v", after)
	}
	if evs, _ := s.QueryEvents(EventFilter{Node: nodeA, Kind: "node.connected"}); evs[0].Hostname != "h1" {
		t.Errorf("hostname missing: %+v", evs[0])
	}
}

func TestAdminEventsEndpoint(t *testing.T) {
	gw, _ := newInstallGateway(t)
	gw.store.Event("node.connected", "", "s_x", "", "d1")
	gw.store.Event("msg.stored", "", "s_x", "m_1", "d2")
	h := gw.Handler()
	if rec := adminReq(t, h, "GET", "/v1/admin/events", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	get := func(q string) []protocol.Event {
		rec := adminReq(t, h, "GET", "/v1/admin/events"+q, gw.adminToken)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", q, rec.Code, rec.Body)
		}
		var r struct {
			Events []protocol.Event `json:"events"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		return r.Events
	}
	if evs := get("?kind=msg"); len(evs) != 1 || evs[0].Detail != "d2" || evs[0].MessageID != "m_1" {
		t.Fatalf("kind filter: %+v", evs)
	}
	if evs := get("?session=s_x&limit=1"); len(evs) != 1 || evs[0].Kind != "msg.stored" {
		t.Fatalf("limit: %+v", evs)
	}
	if evs := get("?since=2999-01-01T00:00:00Z"); len(evs) != 0 {
		t.Fatalf("since: %+v", evs)
	}
	for _, q := range []string{"?since=yesterday", "?after_id=x", "?limit=-1"} {
		if rec := adminReq(t, h, "GET", "/v1/admin/events"+q, gw.adminToken); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
}

// ---- retention ----

func TestPruneDeletesOldDataOnly(t *testing.T) {
	s, c, lc := newLoggedStore(t)
	a, b := twoSessions(t, s)
	nodeA := mustNode(t, s, a)
	day := 24 * time.Hour

	old, _, _ := send(s, a, b, "old", "")
	s.ApplyDelivery(old.ID, protocol.StateDelivered, "")
	s.Event("custom.old", "", "", "", "")
	// An offline session last seen long ago, with a profile.
	gone, _ := s.UpsertSession(nodeA, protocol.SessionInfo{Harness: "claude", NativeID: "gone", Name: "gone"})
	s.Announce(nodeA, protocol.ProfileAnnounce{SessionID: gone, Name: "gone"})
	s.SetSessionOffline(nodeA, gone)

	c.t = c.t.Add(40 * day)
	fresh, _, _ := send(s, a, b, "fresh", "")
	s.ApplyDelivery(fresh.ID, protocol.StateDelivered, "")
	s.Event("custom.fresh", "", "", "", "")
	// Online sessions stay however old; recently offline ones stay.
	recent, _ := s.UpsertSession(nodeA, protocol.SessionInfo{Harness: "claude", NativeID: "recent"})
	s.SetSessionOffline(nodeA, recent)

	res, err := s.Prune(Retention{Messages: 30 * day, Events: 90 * day, Sessions: 30 * day})
	if err != nil {
		t.Fatal(err)
	}
	if res.Messages != 1 || res.Deliveries != 1 || res.Sessions != 1 || res.Events != 0 {
		t.Fatalf("result %+v", res)
	}
	if _, ok, _ := s.Message(old.ID); ok {
		t.Fatal("old message kept")
	}
	if m, ok, _ := s.Message(fresh.ID); !ok || m.Body != "hi" {
		t.Fatal("fresh message gone")
	}
	count := func(q string) int {
		var n int
		if err := s.db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM deliveries`); n != 1 {
		t.Fatalf("deliveries left: %d", n)
	}
	if _, ok, _ := s.Agent(gone); ok {
		t.Fatal("old offline session kept")
	}
	if n := count(`SELECT COUNT(*) FROM profiles WHERE session_id = '` + gone + `'`); n != 0 {
		t.Fatal("profile of a deleted session kept")
	}
	for _, id := range []string{a, b, recent} {
		if _, ok, _ := s.Agent(id); !ok {
			t.Fatalf("session %s deleted", id)
		}
	}
	if ls := lc.with("event=retention", "messages=1", "deliveries=1", "sessions=1"); len(ls) != 1 {
		t.Fatalf("retention log: %v", lc.all())
	}
	if evs, _ := s.QueryEvents(EventFilter{Kind: "retention"}); len(evs) != 1 {
		t.Fatalf("retention event: %+v", evs)
	}

	// Events: 100 days later the old ones go, and the sessions a and b (online) stay.
	c.t = c.t.Add(60 * day)
	res, _ = s.Prune(Retention{Events: 90 * day})
	if res.Events == 0 {
		t.Fatalf("no events pruned: %+v", res)
	}
	if evs, _ := s.QueryEvents(EventFilter{Kind: "custom.old"}); len(evs) != 0 {
		t.Fatal("old event kept")
	}
	if evs, _ := s.QueryEvents(EventFilter{Kind: "custom.fresh"}); len(evs) != 1 {
		t.Fatal("event younger than 90 days deleted")
	}
	// A zero period keeps everything.
	c.t = c.t.Add(3650 * day)
	before := count(`SELECT COUNT(*) FROM events`)
	if res, _ := s.Prune(Retention{}); res.Any() || count(`SELECT COUNT(*) FROM events`) != before {
		t.Fatal("zero retention deleted data")
	}
}

func TestOfflineSessionClockStartsWhenItGoesOffline(t *testing.T) {
	// A session that ran for 40 days was last upserted 40 days ago. It must not
	// be deleted the moment it goes offline.
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	id, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "long"})
	c.t = c.t.Add(40 * 24 * time.Hour)
	s.SetSessionOffline(node, id)
	if res, _ := s.Prune(Retention{Sessions: 30 * 24 * time.Hour}); res.Sessions != 0 {
		t.Fatalf("deleted a session that just went offline: %+v", res)
	}
	c.t = c.t.Add(31 * 24 * time.Hour)
	if res, _ := s.Prune(Retention{Sessions: 30 * 24 * time.Hour}); res.Sessions != 1 {
		t.Fatalf("kept a session offline for 31 days: %+v", res)
	}
}

func TestRunRetentionPrunesAtStart(t *testing.T) {
	data := t.TempDir()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	gw, err := New(Options{DataDir: data, Now: func() time.Time { return now }, Retention: DefaultRetention()})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	gw.store.db.Exec(`INSERT INTO events (at, kind) VALUES (?, 'ancient')`, now.Add(-100*24*time.Hour).UnixNano())
	gw.store.db.Exec(`INSERT INTO events (at, kind) VALUES (?, 'recent')`, now.Add(-10*24*time.Hour).UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go gw.RunRetention(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for {
		evs, _ := gw.store.QueryEvents(EventFilter{Kind: "ancient"})
		if len(evs) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("RunRetention did not prune at start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if evs, _ := gw.store.QueryEvents(EventFilter{Kind: "recent"}); len(evs) != 1 {
		t.Fatal("recent event deleted")
	}
}

// ---- harness inventory ----

func TestSetNodeHarnesses(t *testing.T) {
	s, _, lc := newLoggedStore(t)
	id, _, _ := s.CreateNode("box", "linux")
	s.SetNodeHello(id, "box", "v0.4.0")

	nodes, _ := s.Nodes()
	if nodes[0].Harnesses != nil {
		t.Fatalf("a node that never reported must have nil harnesses: %#v", nodes[0].Harnesses)
	}
	// Not reported (nil) changes nothing and logs nothing.
	if ch, err := s.SetNodeHarnesses(id, nil); err != nil || ch != "" || len(lc.all()) != 0 {
		t.Fatalf("nil: %q %v %v", ch, err, lc.all())
	}

	first := []protocol.HarnessInfo{{Name: "claude", Path: "/u/claude", Version: "2.1.0"},
		{Name: "opencode", Path: "/u/opencode", Version: "v1", Models: []string{"a/x", "b/y"}}}
	ch, err := s.SetNodeHarnesses(id, first)
	if err != nil || ch != "+claude,+opencode" {
		t.Fatalf("first: %q %v", ch, err)
	}
	if ls := lc.with("event=node.harnesses", "node="+id, "host=box", "change=+claude,+opencode", "installed=claude,opencode"); len(ls) != 1 {
		t.Fatalf("log: %v", lc.all())
	}
	nodes, _ = s.Nodes()
	if !reflect.DeepEqual(nodes[0].Harnesses, first) {
		t.Fatalf("stored: %+v", nodes[0].Harnesses)
	}

	// The same inventory again: no change, no line, no event.
	if ch, _ := s.SetNodeHarnesses(id, first); ch != "" {
		t.Fatalf("same inventory reported a change: %q", ch)
	}
	if n := len(lc.with("event=node.harnesses")); n != 1 {
		t.Fatalf("lines: %d", n)
	}

	second := []protocol.HarnessInfo{{Name: "claude", Path: "/u/claude", Version: "2.2.0"},
		{Name: "opencode", Path: "/u/opencode", Version: "v1", Models: []string{"a/x", "b/y", "c/z"}},
		{Name: "pi", Path: "/u/pi", Version: "1.4.0"}}
	ch, _ = s.SetNodeHarnesses(id, second)
	if ch != "claude:2.1.0->2.2.0,opencode:models 2->3,+pi" {
		t.Fatalf("change: %q", ch)
	}
	if ls := lc.with("event=node.harnesses", `change="claude:2.1.0->2.2.0,opencode:models 2->3,+pi"`); len(ls) != 1 {
		t.Fatalf("log: %v", lc.all())
	}

	// An empty list is a report: nothing installed. Nil still means "not reported".
	ch, _ = s.SetNodeHarnesses(id, []protocol.HarnessInfo{})
	if ch != "-claude,-opencode,-pi" {
		t.Fatalf("removal: %q", ch)
	}
	nodes, _ = s.Nodes()
	if nodes[0].Harnesses == nil || len(nodes[0].Harnesses) != 0 {
		t.Fatalf("stored empty list: %#v", nodes[0].Harnesses)
	}
	if evs, _ := s.QueryEvents(EventFilter{Kind: "node.harnesses"}); len(evs) != 3 {
		t.Fatalf("events: %+v", evs)
	}
}

func TestSetNodeHarnessesFirstReportEmpty(t *testing.T) {
	s, _, lc := newLoggedStore(t)
	id, _, _ := s.CreateNode("box", "linux")
	s.SetNodeHello(id, "box", "v0.4.0")
	if ch, _ := s.SetNodeHarnesses(id, []protocol.HarnessInfo{}); ch != "none installed" {
		t.Fatalf("got %q", ch)
	}
	if len(lc.with(`change="none installed"`)) != 1 {
		t.Fatalf("log: %v", lc.all())
	}
	nodes, _ := s.Nodes()
	if nodes[0].Harnesses == nil {
		t.Fatal("an empty report must not read as 'not reported'")
	}
}

func TestDescribeHarnessChange(t *testing.T) {
	h := func(name, path, ver string, models ...string) protocol.HarnessInfo {
		return protocol.HarnessInfo{Name: name, Path: path, Version: ver, Models: models}
	}
	cases := []struct {
		old, cur []protocol.HarnessInfo
		want     string
	}{
		{nil, nil, ""},
		{[]protocol.HarnessInfo{h("pi", "/a", "1")}, []protocol.HarnessInfo{h("pi", "/a", "1")}, ""},
		{[]protocol.HarnessInfo{h("pi", "/a", "1")}, []protocol.HarnessInfo{h("pi", "/b", "1")}, "pi:path"},
		{[]protocol.HarnessInfo{h("pi", "/a", "")}, []protocol.HarnessInfo{h("pi", "/a", "2")}, "pi:?->2"},
		{[]protocol.HarnessInfo{h("opencode", "/a", "1", "a/x")}, []protocol.HarnessInfo{h("opencode", "/a", "1", "a/y")}, "opencode:models changed"},
		{[]protocol.HarnessInfo{h("codex", "/a", "1")}, nil, "-codex"},
		{[]protocol.HarnessInfo{h("pi", "/a", "1")},
			[]protocol.HarnessInfo{{Name: "pi", Path: "/a", Version: "1", Others: []protocol.HarnessInstall{{Path: "/b", Version: "0.9"}}}}, "pi:installs 1->2"},
	}
	for i, c := range cases {
		if got := DescribeHarnessChange(c.old, c.cur); got != c.want {
			t.Errorf("case %d: got %q, want %q", i, got, c.want)
		}
	}
}

func TestHelloWithoutHarnessesDecodesAsNotReported(t *testing.T) {
	// What a daemon from before the inventory sends.
	var p protocol.Hello
	if err := json.Unmarshal([]byte(`{"type":"hello","id":"1","version":"v0.3.2","hostname":"old"}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.Harnesses != nil {
		t.Fatalf("absent harnesses must decode to nil: %#v", p.Harnesses)
	}
	if err := json.Unmarshal([]byte(`{"harnesses":[]}`), &p); err != nil || p.Harnesses == nil {
		t.Fatalf("an empty list must stay non-nil: %#v %v", p.Harnesses, err)
	}
	// And a new gateway answers a daemon that sends no repo fields: they are empty.
	var si protocol.SessionInfo
	if err := json.Unmarshal([]byte(`{"harness":"claude","native_id":"n"}`), &si); err != nil || si.Repo != (protocol.RepoInfo{}) {
		t.Fatalf("session without repo: %+v %v", si, err)
	}
}

// ---- migration and file modes ----

func TestMigration4KeepsExistingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gw.db")
	// A database as version 0.3.2 left it: migrations 1 to 3 applied.
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatal(err)
		}
		db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1)
	}
	db.Exec(`INSERT INTO nodes (id, hostname, os, token_hash, created_at, version, seen_at) VALUES ('n_1','old','linux','h',1,'v0.3.2',5)`)
	db.Exec(`INSERT INTO sessions (id, node_id, harness, native_id, cwd, last_seen, online) VALUES ('s_1','n_1','claude','x','/w/p',5,0)`)
	db.Close()

	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.SchemaVersion(); v != len(migrations) || len(migrations) < 4 {
		t.Fatalf("schema version %d of %d", v, len(migrations))
	}
	nodes, _ := s.Nodes()
	if len(nodes) != 1 || nodes[0].Hostname != "old" || nodes[0].Harnesses != nil {
		t.Fatalf("nodes: %+v", nodes)
	}
	a, ok, _ := s.Agent("s_1")
	if !ok || a.Cwd != "/w/p" || a.Repo != (protocol.RepoInfo{}) {
		t.Fatalf("agent: %+v", a)
	}
	// The project list names an old session after its folder.
	ps, _ := s.Projects("")
	if len(ps) != 1 || ps[0].Name != "p" || ps[0].Path != "/w/p" {
		t.Fatalf("projects: %+v", ps)
	}
}

func modeOf(t *testing.T, p string) os.FileMode {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	return st.Mode().Perm()
}

func TestDatabaseFilesArePrivate(t *testing.T) {
	old := setUmask(0o022)
	defer setUmask(old)
	path := filepath.Join(t.TempDir(), "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	node, _, _ := s.CreateNode("h", "linux") // a write, so the -wal file has content
	if node == "" {
		t.Fatal("no node")
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if m := modeOf(t, p); m != 0o600 {
			t.Errorf("%s has mode %o, want 600", filepath.Base(p), m)
		}
	}
	s.Close()

	// Files made by an older version with the default mode are fixed at start.
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(p); err != nil {
			os.WriteFile(p, nil, 0o644)
		}
		os.Chmod(p, 0o644)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if m := modeOf(t, p); m != 0o600 {
			t.Errorf("after reopen %s has mode %o, want 600", filepath.Base(p), m)
		}
	}
}

func TestGatewayNewKeepsDatabasePrivate(t *testing.T) {
	data := t.TempDir()
	gw, err := New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	if m := modeOf(t, filepath.Join(data, "gateway.db")); m != 0o600 {
		t.Fatalf("gateway.db has mode %o", m)
	}
}

func setUmask(m int) int { return syscall.Umask(m) }

// Sessions in the main tree and in linked worktrees make one project per
// machine. The worktree folders are kept as a list.
func TestProjectsGroupWorktrees(t *testing.T) {
	s, _ := newTestStore(t)
	n1, _, _ := s.CreateNode("mac", "darwin")
	n2, _, _ := s.CreateNode("box", "linux")
	main := protocol.RepoInfo{Root: "/src/tenebrae", Worktree: "/src/tenebrae", Project: "tenebrae", Remote: "https://example.com/t.git", Branch: "main"}
	wt1 := protocol.RepoInfo{Root: "/src/tenebrae", Worktree: "/wt/agent-a76c", Project: "tenebrae", Remote: "https://example.com/t.git", Branch: "x"}
	wt2 := protocol.RepoInfo{Root: "/src/tenebrae", Worktree: "/wt/agent-b11d", Project: "tenebrae", Branch: "y"}
	s.UpsertSession(n1, protocol.SessionInfo{Harness: "claude", NativeID: "a", Cwd: "/src/tenebrae", Repo: main})
	s.UpsertSession(n1, protocol.SessionInfo{Harness: "claude", NativeID: "b", Cwd: "/wt/agent-a76c/pkg", Repo: wt1})
	s.UpsertSession(n1, protocol.SessionInfo{Harness: "opencode", NativeID: "c", Cwd: "/wt/agent-b11d", Repo: wt2})
	s.UpsertSession(n2, protocol.SessionInfo{Harness: "pi", NativeID: "d", Cwd: "/home/u/tenebrae",
		Repo: protocol.RepoInfo{Root: "/home/u/tenebrae", Worktree: "/home/u/tenebrae", Project: "tenebrae"}})
	ps, err := s.Projects("tenebrae")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("want one project per machine, got %+v", ps)
	}
	var mac protocol.ProjectView
	for _, p := range ps {
		if p.Hostname == "mac" {
			mac = p
		}
	}
	if mac.Path != "/src/tenebrae" || mac.Sessions != 3 || strings.Join(mac.Harnesses, ",") != "claude,opencode" ||
		strings.Join(mac.Worktrees, ",") != "/wt/agent-a76c,/wt/agent-b11d" {
		t.Fatalf("mac project: %+v", mac)
	}
	for _, p := range ps {
		if p.Hostname == "box" && len(p.Worktrees) != 0 {
			t.Fatalf("a main tree has no linked worktrees: %+v", p)
		}
	}
}
