package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A session under a provider usage limit cannot answer: the store keeps what
// its daemon reports, and the reminder waits for the reset.
func TestReminderSkipsLimitedSessionUntilReset(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "help", Type: protocol.TypeRequest}); err != nil {
		t.Fatal(err)
	}
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, b).Scan(&node); err != nil {
		t.Fatal(err)
	}
	until := c.t.Add(102 * time.Minute)
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "nb", Name: "bob", Status: "idle",
		LimitedUntil: until.Unix(), LimitedReason: "ChatGPT usage limit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", c.t.UnixNano(), b); err != nil {
		t.Fatal(err)
	}
	v, ok, err := s.Agent(b)
	if err != nil || !ok || v.LimitedUntil != until.Unix() || v.LimitedReason != "ChatGPT usage limit" || !v.Limited(c.t) {
		t.Fatalf("view = %+v %v %v", v, ok, err)
	}
	c.t = c.t.Add(3 * time.Minute)
	if r, err := s.remindPending(); err != nil || len(r) != 0 {
		t.Fatalf("a limited session got a reminder: %+v %v", r, err)
	}
	c.t = until.Add(time.Minute)
	if r, err := s.remindPending(); err != nil || len(r) != 1 {
		t.Fatalf("no reminder after the reset: %+v %v", r, err)
	}
}

// A daemon that reports no limit clears it.
func TestLimitClearsWhenDaemonReportsNone(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, b).Scan(&node); err != nil {
		t.Fatal(err)
	}
	info := protocol.SessionInfo{Harness: "pi", NativeID: "nb", Name: "bob", LimitedUntil: c.t.Add(time.Hour).Unix(), LimitedReason: "usage limit"}
	if _, err := s.UpsertSession(node, info); err != nil {
		t.Fatal(err)
	}
	info.LimitedUntil, info.LimitedReason = 0, ""
	if _, err := s.UpsertSession(node, info); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := s.Agent(b); v.LimitedUntil != 0 || v.LimitedReason != "" {
		t.Fatalf("limit kept: %+v", v)
	}
}

func TestIdleCheckSkipsLimitedSession(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	_ = a
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, b).Scan(&node); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", c.t.UnixNano(), b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO profiles(session_id,name,function,project,capabilities,task,updated_at,expertise,task_started) VALUES(?,'bob','','','[]','build the thing',?,'',?) ON CONFLICT(session_id) DO UPDATE SET task='build the thing'`, b, c.t.UnixNano(), c.t.UnixNano()); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(idleCheckAfter + time.Minute)
	until := c.t.Add(time.Hour)
	if _, err := s.db.Exec(`UPDATE sessions SET limited_until=?,limited_reason='usage limit' WHERE id=?`, until.Unix(), b); err != nil {
		t.Fatal(err)
	}
	out, err := s.checkIdle()
	if err != nil || len(out) != 0 {
		t.Fatalf("a limited session was checked: %+v %v", out, err)
	}
	c.t = until.Add(time.Minute)
	if _, err := s.db.Exec("UPDATE sessions SET last_seen=?, last_active=? WHERE id=?", c.t.UnixNano(), c.t.Add(-idleCheckAfter-time.Minute).UnixNano(), b); err != nil {
		t.Fatal(err)
	}
	out, err = s.checkIdle()
	if err != nil || len(out) != 1 {
		t.Fatalf("no check after the reset: %+v %v", out, err)
	}
}

func TestLeadRoutePrefersSessionWithoutLimit(t *testing.T) {
	s := teamStore(t)
	var lead, outsider, node, agent string
	if err := s.db.QueryRow(`SELECT id,node_id,agent_id FROM sessions WHERE repo_id='demo'`).Scan(&lead, &node, &agent); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='other'`).Scan(&outsider); err != nil {
		t.Fatal(err)
	}
	if err := s.AddArchitect("helper", "owner"); err != nil {
		t.Fatal(err)
	}
	target, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "helper", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id='helper',name='Helper' WHERE id=?`, target); err != nil {
		t.Fatal(err)
	}
	second, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "lead2", Name: "Second", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id=?,repo_id='demo' WHERE id=?`, agent, second); err != nil {
		t.Fatal(err)
	}
	until := s.now().Add(time.Hour).Unix()
	limit := func(id string, on bool) {
		t.Helper()
		v := int64(0)
		if on {
			v = until
		}
		if _, err := s.db.Exec(`UPDATE sessions SET limited_until=?,limited_reason='ChatGPT usage limit' WHERE id=?`, v, id); err != nil {
			t.Fatal(err)
		}
	}
	send := func() (protocol.Message, string) {
		t.Helper()
		m, _, route, e := s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: target, Body: "hello"})
		if e != nil {
			t.Fatal(e)
		}
		return m, route
	}
	limit(lead, true)
	if m, route := send(); m.To != second || !strings.Contains(route, "Redirected") {
		t.Fatalf("limited lead chosen: to=%s route=%q", m.To, route)
	}
	limit(second, true)
	if m, route := send(); m.To != lead || !strings.Contains(route, "hit its usage limit; it can answer after 13:00 UTC") {
		t.Fatalf("all limited: to=%s route=%q", m.To, route)
	}
	limit(lead, false)
	limit(second, false)
	if m, route := send(); m.To != lead || strings.Contains(route, "usage limit") {
		t.Fatalf("no limit: to=%s route=%q", m.To, route)
	}
}

func TestReceiptNamesLimitedRecipient(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	until := c.t.Add(102 * time.Minute) // 13:42 UTC
	if _, err := s.db.Exec(`UPDATE sessions SET limited_until=?,limited_reason='ChatGPT usage limit' WHERE id=?`, until.Unix(), b); err != nil {
		t.Fatal(err)
	}
	m, _, route, err := s.StoreLeadMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != b || m.State != protocol.StateStored {
		t.Fatalf("message not delivered as addressed: %+v", m)
	}
	if want := "bob hit its usage limit; it can answer after 13:42 UTC"; route != want {
		t.Fatalf("route = %q, want %q", route, want)
	}
	c.t = until.Add(time.Minute)
	if _, _, route, err = s.StoreLeadMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "again"}); err != nil || route != "" {
		t.Fatalf("limit over: route=%q err=%v", route, err)
	}
}
