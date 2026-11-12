package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type fireRow struct {
	kind, recipient, agent, team, messageID string
	firedAt, followedAt                     int64
	followedKind                            string
}

func nudgeFires(t *testing.T, s *Store) []fireRow {
	t.Helper()
	rows, err := s.db.Query(`SELECT kind,recipient,agent_id,team_id,message_id,fired_at,followed_at,followed_kind
 FROM nudge_fires ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []fireRow
	for rows.Next() {
		var r fireRow
		if err := rows.Scan(&r.kind, &r.recipient, &r.agent, &r.team, &r.messageID, &r.firedAt, &r.followedAt, &r.followedKind); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func oneFire(t *testing.T, s *Store, kind string) fireRow {
	t.Helper()
	for _, r := range nudgeFires(t, s) {
		if r.kind == kind {
			return r
		}
	}
	t.Fatalf("no %s fire: %+v", kind, nudgeFires(t, s))
	return fireRow{}
}

func TestNudgeFireRecordedForEveryKind(t *testing.T) {
	t.Run("idle_check", func(t *testing.T) {
		s, c := newTestStore(t)
		a, b := twoSessions(t, s)
		if _, _, err := send(s, a, b, "req", ""); err != nil {
			t.Fatal(err)
		}
		idleSince(t, s, c, b, 21*time.Minute)
		got := checks(t, s)
		if len(got) != 1 {
			t.Fatalf("checks = %+v", got)
		}
		r := oneFire(t, s, "idle_check")
		if r.recipient != b || r.messageID != got[0].ID {
			t.Fatalf("fire = %+v, message %s", r, got[0].ID)
		}
	})

	t.Run("autonomy_continue", func(t *testing.T) {
		s, c := newTestStore(t)
		a, b := twoSessions(t, s)
		setAutonomy(t, s, b, protocol.AutonomyWake)
		if _, _, err := send(s, a, b, "req", ""); err != nil {
			t.Fatal(err)
		}
		idleSince(t, s, c, b, 21*time.Minute)
		checks(t, s)
		c.t = c.t.Add(time.Minute)
		if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "more: finish it"}); err != nil {
			t.Fatal(err)
		}
		wakes := wakeMessages(t, s)
		if len(wakes) != 1 {
			t.Fatalf("wakes = %+v", wakes)
		}
		r := oneFire(t, s, "autonomy_continue")
		if r.recipient != b || r.messageID != wakes[0].ID {
			t.Fatalf("fire = %+v, wake %s", r, wakes[0].ID)
		}
	})

	t.Run("reply_reminder", func(t *testing.T) {
		s, c := newTestStore(t)
		a, b := twoSessions(t, s)
		if _, _, err := send(s, a, b, "req", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", c.t.UnixNano(), b); err != nil {
			t.Fatal(err)
		}
		c.t = c.t.Add(3 * time.Minute)
		reminders, err := s.remindPending()
		if err != nil || len(reminders) != 1 {
			t.Fatalf("reminders = %+v, %v", reminders, err)
		}
		r := oneFire(t, s, "reply_reminder")
		if r.recipient != b || r.messageID != reminders[0].ID {
			t.Fatalf("fire = %+v, reminder %s", r, reminders[0].ID)
		}
	})

	t.Run("loop_tick", func(t *testing.T) {
		s, c := newTestStore(t)
		_, b := twoSessions(t, s)
		loopNode(t, s, c, b)
		loopTicks(t, s)
		res := runLoops(t, s, c)
		if len(res) != 1 || res[0].Message == nil {
			t.Fatalf("loops = %+v", res)
		}
		r := oneFire(t, s, "loop_tick")
		if r.recipient != b || r.messageID != res[0].Message.ID {
			t.Fatalf("fire = %+v, loop %s", r, res[0].Message.ID)
		}
	})

	t.Run("step_reminder", func(t *testing.T) {
		r := setupMergerRig(t)
		q := r.workerSubmit("feature/fire-reminder", commitA)
		r.submit(r.a, q)
		r.g.dispatchStepTasks(context.Background())
		r.clk.t = r.clk.t.Add(2*time.Hour + 30*time.Minute)
		r.g.dispatchStepTasks(context.Background())
		fire := oneFire(t, r.s, "step_reminder")
		var body string
		if err := r.s.db.QueryRow(`SELECT body FROM messages WHERE id=?`, fire.messageID).Scan(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) == 0 || body[0] != '[' {
			t.Fatalf("step reminder message %q", body)
		}
	})

	t.Run("stuck_step", func(t *testing.T) {
		r := setupMergerRig(t)
		q := r.workerSubmit("feature/fire-stuck", commitA)
		r.submit(r.a, q)
		r.g.dispatchStepTasks(context.Background())
		r.clk.t = r.clk.t.Add(13 * time.Hour)
		r.g.checkStuckStepTasks(context.Background(), 12*time.Hour)
		fire := oneFire(t, r.s, "stuck_step")
		if fire.recipient == "" || fire.messageID == "" {
			t.Fatalf("fire = %+v", fire)
		}
	})

	t.Run("undelivered", func(t *testing.T) {
		s, _ := newTestStore(t)
		a, b := twoSessions(t, s)
		m := sendBody(t, s, a, b, "perm", "hello")
		if err := s.ApplyDelivery(m.ID, protocol.StateFailed, "plugin refused: busy"); err != nil {
			t.Fatal(err)
		}
		n := oneNotice(t, s, a)
		fire := oneFire(t, s, "undelivered")
		if fire.recipient != a || fire.messageID != n.ID {
			t.Fatalf("fire = %+v, notice %s", fire, n.ID)
		}
	})

	t.Run("waiting_notice", func(t *testing.T) {
		s, c := newTestStore(t)
		a, b := twoSessions(t, s)
		setWait(t, s, b, &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "q", Since: c.t.Add(-time.Minute).Unix()})
		_, notice, noticed := deliver(t, s, a, b, "first")
		if !noticed {
			t.Fatal("no waiting notice")
		}
		fire := oneFire(t, s, "waiting_notice")
		if fire.recipient != a || fire.messageID != notice.ID {
			t.Fatalf("fire = %+v, notice %s", fire, notice.ID)
		}
	})

	t.Run("wake_failed", func(t *testing.T) {
		s, _ := newTestStore(t)
		a, b := twoSessions(t, s)
		sendBody(t, s, a, b, "queued", "hello")
		notices, err := s.failWake(b, "attempt-1", "no such host")
		if err != nil || len(notices) != 1 {
			t.Fatalf("notices = %+v, %v", notices, err)
		}
		fire := oneFire(t, s, "wake_failed")
		if fire.recipient != a || fire.messageID != notices[0].ID {
			t.Fatalf("fire = %+v, notice %s", fire, notices[0].ID)
		}
	})

	t.Run("member_blocked", func(t *testing.T) {
		s, _, member, lead := leadTeamRig(t)
		announce(t, s, member, protocol.ProfileAnnounce{Task: deniedMerge})
		notices := blockedNotices(t, s)
		if len(notices) != 1 {
			t.Fatalf("notices = %+v", notices)
		}
		fire := oneFire(t, s, "member_blocked")
		if fire.recipient != lead || fire.messageID != notices[0].ID {
			t.Fatalf("fire = %+v, notice %s", fire, notices[0].ID)
		}
	})
}

// idleCheckFire makes one idle check for b and returns its fire.
func idleCheckFire(t *testing.T, s *Store, c *clock) (a, b string, fire fireRow) {
	t.Helper()
	a, b = twoSessions(t, s)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("checks = %+v", got)
	}
	return a, b, oneFire(t, s, "idle_check")
}

func TestNudgeFollowedByToolCallAnnounceAndMessage(t *testing.T) {
	for _, tc := range []struct {
		name string
		work func(t *testing.T, s *Store, c *clock, a, b string)
		kind string
	}{
		{"tool_call", func(t *testing.T, s *Store, c *clock, a, b string) {
			if err := s.TouchNudgeWork(b, c.t.Add(5*time.Minute).UnixNano()); err != nil {
				t.Fatal(err)
			}
		}, "tool_call"},
		{"announce", func(t *testing.T, s *Store, c *clock, a, b string) {
			c.t = c.t.Add(5 * time.Minute)
			if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "working"}); err != nil {
				t.Fatal(err)
			}
		}, "announce"},
		{"message", func(t *testing.T, s *Store, c *clock, a, b string) {
			c.t = c.t.Add(5 * time.Minute)
			if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "working on it"}); err != nil {
				t.Fatal(err)
			}
		}, "message"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c := newTestStore(t)
			a, b, fire := idleCheckFire(t, s, c)
			c.t = c.t.Add(6 * time.Minute)
			tc.work(t, s, c, a, b)
			out, err := s.NudgeStats(protocol.NudgeStatsArgs{})
			if err != nil {
				t.Fatal(err)
			}
			if out.Fires != 1 || out.Followed != 1 {
				t.Fatalf("stats = %+v", out)
			}
			r := oneFire(t, s, "idle_check")
			if r.followedAt == 0 || r.followedKind != tc.kind || r.followedAt < fire.firedAt {
				t.Fatalf("follow = %+v, fire = %+v", r, fire)
			}
		})
	}
}

func TestNudgeAckDoesNotCountAsWork(t *testing.T) {
	s, c := newTestStore(t)
	a, b, _ := idleCheckFire(t, s, c)
	c.t = c.t.Add(5 * time.Minute)
	req, _, err := send(s, a, b, "another", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Ack: true}); err != nil {
		t.Fatal(err)
	}
	out, err := s.NudgeStats(protocol.NudgeStatsArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Followed != 0 {
		t.Fatalf("ack counted as work: %+v", out)
	}
}

func TestNudgeReplyAfterWindowDoesNotCount(t *testing.T) {
	s, c := newTestStore(t)
	a, b, _ := idleCheckFire(t, s, c)
	c.t = c.t.Add(31 * time.Minute)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "late"}); err != nil {
		t.Fatal(err)
	}
	out, err := s.NudgeStats(protocol.NudgeStatsArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Followed != 0 {
		t.Fatalf("a reply after the window counted: %+v", out)
	}
}

func TestNudgeStatsAggregatePerKindAgentAndTeam(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	carol := thirdSession(t, s)
	for _, x := range []struct{ session, agent, team string }{
		{a, "agent-a", "team-1"},
		{b, "agent-b", "team-1"},
		{carol, "agent-c", "team-2"},
	} {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO agents(id,display_name,created_at,updated_at) VALUES(?,?,0,0)`, x.agent, x.agent+" name"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE sessions SET agent_id=? WHERE id=?`, x.agent, x.session); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO teams(id,name,created_at,updated_at) VALUES(?,?,0,0)`, x.team, x.team); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO team_members(team_id,member_kind,member_id) VALUES(?,'agent',?)`, x.team, x.agent); err != nil {
			t.Fatal(err)
		}
	}
	at := c.t.UnixNano()
	for i, x := range []struct {
		kind, recipient, message string
		followed                 bool
	}{
		{"idle_check", b, "m1", true},
		{"idle_check", b, "m2", false},
		{"idle_check", a, "m3", true},
		{"reply_reminder", a, "m4", false},
	} {
		if _, err := s.db.Exec(`INSERT INTO nudge_fires(kind,recipient,agent_id,team_id,message_id,fired_at,followed_at,followed_kind)
 SELECT ?,id,COALESCE(agent_id,''),COALESCE((SELECT tm.team_id FROM team_members tm WHERE (tm.member_kind='agent' AND tm.member_id=sessions.agent_id) ORDER BY tm.team_id LIMIT 1),''),?,?,?,?
 FROM sessions WHERE id=?`, x.kind, x.message, at, map[bool]int64{true: at + int64(time.Minute), false: 0}[x.followed], map[bool]string{true: "tool_call", false: ""}[x.followed], x.recipient); err != nil {
			t.Fatal(err)
		}
		_ = i
	}
	out, err := s.NudgeStats(protocol.NudgeStatsArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Fires != 4 || out.Followed != 2 {
		t.Fatalf("totals = %+v", out)
	}
	wantAgent := map[string][2]int{
		"idle_check/agent-b":     {2, 1},
		"idle_check/agent-a":     {1, 1},
		"reply_reminder/agent-a": {1, 0},
	}
	for _, row := range out.Kinds {
		key := row.Kind + "/" + row.Agent
		if want, ok := wantAgent[key]; ok {
			if row.Fires != want[0] || row.Followed != want[1] {
				t.Errorf("%s = %d/%d, want %d/%d", key, row.Followed, row.Fires, want[1], want[0])
			}
			delete(wantAgent, key)
		}
	}
	if len(wantAgent) != 0 {
		t.Errorf("missing agent rows: %v in %+v", wantAgent, out.Kinds)
	}
	wantTeam := map[string][2]int{
		"idle_check/team-1":     {3, 2},
		"reply_reminder/team-1": {1, 0},
	}
	for _, row := range out.Teams {
		key := row.Kind + "/" + row.Team
		if want, ok := wantTeam[key]; ok {
			if row.Fires != want[0] || row.Followed != want[1] {
				t.Errorf("%s = %d/%d, want %d/%d", key, row.Followed, row.Fires, want[1], want[0])
			}
			delete(wantTeam, key)
		}
	}
	if len(wantTeam) != 0 {
		t.Errorf("missing team rows: %v in %+v", wantTeam, out.Teams)
	}
}

func TestNudgeBatchedEnvelopeWritesARowPerPart(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	// Two open requests make two reminders, each its own nudge.
	for _, key := range []string{"r1", "r2"} {
		if _, _, err := send(s, a, b, key, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec("UPDATE sessions SET status='idle',last_active=? WHERE id=?", c.t.UnixNano(), b); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(3 * time.Minute)
	reminders, err := s.remindPending()
	if err != nil || len(reminders) != 2 {
		t.Fatalf("reminders = %+v, %v", reminders, err)
	}
	if _, err := s.db.Exec("UPDATE sessions SET status='busy' WHERE id=?", b); err != nil {
		t.Fatal(err)
	}
	for _, m := range reminders {
		if !s.HoldForBusy(m) {
			t.Fatalf("message %s was not held", m.ID)
		}
	}
	if _, err := s.db.Exec("UPDATE sessions SET status='idle' WHERE id=?", b); err != nil {
		t.Fatal(err)
	}
	carrier, n, err := releaseOne(t, s, b, false)
	if err != nil || n != 2 || len(carrier.Batch) != 2 {
		t.Fatalf("release: carrier=%+v n=%d err=%v", carrier, n, err)
	}
	fires := nudgeFires(t, s)
	if len(fires) != 2 {
		t.Fatalf("fires = %+v, want one per part", fires)
	}
	for _, r := range fires {
		if r.kind != "reply_reminder" {
			t.Fatalf("fire = %+v", r)
		}
	}
}

func TestNudgePausePreviewShowsUsefulLine(t *testing.T) {
	s, c, chief, target := nudgeSetup(t)
	if _, _, err := send(s, chief, target, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, target, 21*time.Minute)
	got := checks(t, s)
	if len(got) != 1 {
		t.Fatalf("checks = %+v", got)
	}
	if err := s.TouchNudgeWork(target, c.t.Add(time.Minute).UnixNano()); err != nil {
		t.Fatal(err)
	}
	out, err := nudgeCall(t, s, chief, "nudge.pause", map[string]any{"kind": "idle_check", "agent": "target-agent", "mode": "preview"})
	if err != nil {
		t.Fatal(err)
	}
	line, _ := out["useful_line"].(string)
	if line != "useful 1 of 1 in 24h" {
		t.Fatalf("useful line = %q, out %+v", line, out)
	}
}
