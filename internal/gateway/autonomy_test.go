package gateway

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// setAutonomy gives a session a project node and the autonomy setting. The
// project also goes in the profile so a later turn report cannot erase the
// node identity the test set up.
func setAutonomy(t *testing.T, s *Store, session, value string) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project='proj', repo_root='/w/proj' WHERE id=?`, session); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO profiles(session_id,project,updated_at) VALUES(?,'proj',?)
		ON CONFLICT(session_id) DO UPDATE SET project='proj'`, session, s.now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeSetting("proj", protocol.SettingAutonomy, value, "owner"); err != nil {
		t.Fatal(err)
	}
}

// wakeMessages returns the reserved agent-net continue messages in order.
func wakeMessages(t *testing.T, s *Store) []protocol.Message {
	t.Helper()
	rows, err := s.db.Query(`SELECT id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state
		FROM messages WHERE sender='agent-net' AND idem_key LIKE 'wake:%' ORDER BY created_at,rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []protocol.Message
	for rows.Next() {
		var m protocol.Message
		var created, expires int64
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.ParentID, &m.From, &m.To, &m.Type, &m.Body,
			&m.Hop, &m.IdempotencyKey, &created, &expires, &m.State); err != nil {
			t.Fatal(err)
		}
		m.CreatedAt = time.Unix(0, created).UTC()
		m.ExpiresAt = time.Unix(0, expires).UTC()
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func openCheckRows(t *testing.T, s *Store) []protocol.InboxItem {
	t.Helper()
	items, err := s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	var open []protocol.InboxItem
	for _, item := range items {
		if item.Kind == "check" && item.Status == "open" {
			open = append(open, item)
		}
	}
	return open
}

func wantWakeBody(line string) string {
	return fmt.Sprintf("[agent-net] continue: you said you have work left (%s). Carry on, then answer the next check.", line)
}

func TestWakeAutonomySendsOneContinueMessage(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	setAutonomy(t, s, b, protocol.AutonomyWake)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check = %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "more: finish the parser"}); err != nil {
		t.Fatal(err)
	}
	wakes := wakeMessages(t, s)
	if len(wakes) != 1 {
		t.Fatalf("wake messages = %+v, want 1", wakes)
	}
	m := wakes[0]
	if m.From != "agent-net" || m.To != b || m.Type != protocol.TypeNotification || m.Body != wantWakeBody("finish the parser") {
		t.Fatalf("wake = %+v", m)
	}
	if rows := openCheckRows(t, s); len(rows) != 0 {
		t.Fatalf("autonomy wake left a Needs you row: %+v", rows)
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.WakeUpsToday != 1 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

func TestAutonomyOffLeavesTheNeedsYouRow(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	setAutonomy(t, s, b, protocol.AutonomyOff)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check = %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "more: finish the parser"}); err != nil {
		t.Fatal(err)
	}
	if wakes := wakeMessages(t, s); len(wakes) != 0 {
		t.Fatalf("autonomy off woke the session: %+v", wakes)
	}
	rows := openCheckRows(t, s)
	if len(rows) != 1 || !strings.Contains(rows[0].Text, "stopped with work left: finish the parser") {
		t.Fatalf("Needs you rows = %+v", rows)
	}
}

func TestAutonomyWakeDoesNothingOffline(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	setAutonomy(t, s, b, protocol.AutonomyWake)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check = %+v", got)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, b); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "more: finish the parser"}); err != nil {
		t.Fatal(err)
	}
	if wakes := wakeMessages(t, s); len(wakes) != 0 {
		t.Fatalf("offline session was woken: %+v", wakes)
	}
	if rows := openCheckRows(t, s); len(rows) != 0 {
		t.Fatalf("offline wake left a Needs you row: %+v", rows)
	}
}

func TestAutonomyWakeStillLeavesABlockedRow(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	setAutonomy(t, s, b, protocol.AutonomyWake)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check = %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "blocked on the deploy key"}); err != nil {
		t.Fatal(err)
	}
	if wakes := wakeMessages(t, s); len(wakes) != 0 {
		t.Fatalf("blocked session was woken: %+v", wakes)
	}
	rows := openCheckRows(t, s)
	if len(rows) != 1 || !strings.Contains(rows[0].Text, "the deploy key") {
		t.Fatalf("Needs you rows = %+v", rows)
	}
}

func TestWakeDailyCapIsSeparateFromTheCheckCap(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	setAutonomy(t, s, b, protocol.AutonomyWake)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		idleSince(t, s, c, b, 21*time.Minute)
		if got := checks(t, s); len(got) != 1 {
			t.Fatalf("check %d = %+v", i+1, got)
		}
		c.t = c.t.Add(time.Minute)
		if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: fmt.Sprintf("more: step %d", i+1)}); err != nil {
			t.Fatal(err)
		}
		// A new message is progress, so the wake streak and the check streak reset.
		c.t = c.t.Add(time.Minute)
		if _, _, err := send(s, b, a, fmt.Sprintf("progress-%d", i), ""); err != nil {
			t.Fatal(err)
		}
		c.t = c.t.Add(21 * time.Minute)
	}
	if wakes := wakeMessages(t, s); len(wakes) != 6 {
		t.Fatalf("wake messages = %d, want 6", len(wakes))
	}
	// A seventh "more" answer has progress but must not exceed the wake cap.
	// Set the check wait directly so the check cap is not involved.
	if _, err := s.db.Exec(`UPDATE session_checks SET check_waiting=1,check_active=0,check_more=0 WHERE session_id=?`, b); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "more: step 7"}); err != nil {
		t.Fatal(err)
	}
	if wakes := wakeMessages(t, s); len(wakes) != 6 {
		t.Fatalf("wake over the daily cap: %d", len(wakes))
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.WakeUpsToday != 6 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

func TestTwoWakeUpsWithoutProgressStopWakingWithOneRow(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	setAutonomy(t, s, b, protocol.AutonomyWake)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check 1 = %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "more: first step"}); err != nil {
		t.Fatal(err)
	}
	// A turn is activity, but it changes no message and no open work.
	c.t = c.t.Add(time.Minute)
	activity(t, s, b, "pi", "nb")
	c.t = c.t.Add(21 * time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check 2 = %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "more: still first step"}); err != nil {
		t.Fatal(err)
	}
	if wakes := wakeMessages(t, s); len(wakes) != 2 {
		t.Fatalf("wake messages = %d, want 2", len(wakes))
	}
	rows := openCheckRows(t, s)
	if len(rows) != 1 || !strings.Contains(rows[0].Text, "was woken twice with no progress") {
		t.Fatalf("Needs you rows = %+v", rows)
	}
	// A third "more" answer with no progress must not wake again or add a row.
	// Set the check wait directly so the check cap does not decide this.
	if _, err := s.db.Exec(`UPDATE session_checks SET check_waiting=1,check_active=0,check_more=0 WHERE session_id=?`, b); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "more: still first step"}); err != nil {
		t.Fatal(err)
	}
	if wakes := wakeMessages(t, s); len(wakes) != 2 {
		t.Fatalf("waking did not stop: %d", len(wakes))
	}
	if rows := openCheckRows(t, s); len(rows) != 1 {
		t.Fatalf("rows = %d, want one: %+v", len(rows), rows)
	}
	// Progress allows waking again.
	c.t = c.t.Add(time.Minute)
	if _, _, err := send(s, b, a, "progress", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE session_checks SET check_waiting=1,check_active=0,check_more=0 WHERE session_id=?`, b); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "more: second step"}); err != nil {
		t.Fatal(err)
	}
	if wakes := wakeMessages(t, s); len(wakes) != 3 {
		t.Fatalf("waking did not resume after progress: %d", len(wakes))
	}
}

func TestAutonomyShowsInAgentAndGraph(t *testing.T) {
	s, _ := newTestStore(t)
	_, b := twoSessions(t, s)
	setAutonomy(t, s, b, protocol.AutonomyWake)
	a, ok, err := s.Agent(b)
	if err != nil || !ok || a.Charter.Autonomy != protocol.AutonomyWake {
		t.Fatalf("agent charter autonomy = %+v, %v", a, err)
	}
	g, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range g.Nodes {
		if n.Name == "proj" {
			found = n.Settings[protocol.SettingAutonomy] == protocol.AutonomyWake
		}
	}
	if !found {
		t.Fatalf("autonomy missing from the graph: %+v", g.Nodes)
	}
}
