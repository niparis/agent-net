package gateway

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// memberWorker adds a worker that the member started, a finished run for it,
// and (when change is set) an open delivery change that came from it.
func memberWorker(t *testing.T, s *Store, member, change string, endedAt time.Time) string {
	t.Helper()
	node, err := s.SessionNode(member)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "w-" + change, Worker: &protocol.WorkerInfo{ID: "w_" + change, Owner: member}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=0,status='ended',worker_run_active=0,last_seen=? WHERE id=?`, endedAt.UnixNano(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO worker_runs(node_id,session_id,harness,outcome,ended_at,caller_session_id) VALUES(?,?,?,?,?,?)`,
		node, id, "codex", "done", endedAt.Unix(), member); err != nil {
		t.Fatal(err)
	}
	if change != "" {
		if _, err := s.db.Exec(`INSERT INTO delivery_changes(id,project,machine,branch,head_commit,revision,origin,title,state,created_at,updated_at)
			VALUES(?,?,?,?,?,1,?,?,'open',0,0)`, change, "demo", node, "agent-net/"+change, "abc",
			fmt.Sprintf(`{"kind":"worker","session_id":%q}`, id), "Title"); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func acceptChange(t *testing.T, s *Store, change string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO delivery_evidence(change_id,revision,step,result,commit_hash,by_session,at) VALUES(?,1,'accept','pass','abc','s',0)`, change); err != nil {
		t.Fatal(err)
	}
}

// loopLeadRig puts a 4h loop on the lead's node and starts its clock. The
// member is the session of repo demo.
func loopLeadRig(t *testing.T) (s *Store, c *clock, member, lead string) {
	t.Helper()
	s, c, member, lead = leadTeamRig(t)
	loopNode(t, s, c, lead)
	loopTicks(t, s)
	return s, c, member, lead
}

func TestLoopTickShowsABlockedMemberAndAnEndedUnreviewedWorker(t *testing.T) {
	s, c, member, lead := loopLeadRig(t)
	c.t = c.t.Add(10 * time.Minute)
	announce(t, s, member, protocol.ProfileAnnounce{Task: deniedMerge})
	pending := memberWorker(t, s, member, "ch_w1", c.t.Add(time.Minute))
	accepted := memberWorker(t, s, member, "ch_w2", c.t.Add(2*time.Minute))
	acceptChange(t, s, "ch_w2")
	idleSince(t, s, c, lead, time.Minute)
	res := runLoops(t, s, c)
	if len(res) != 1 || res[0].Outcome != protocol.LoopSent {
		t.Fatalf("tick = %+v", res)
	}
	m := loopMessages(t, s)
	if len(m) != 1 {
		t.Fatalf("messages = %+v", m)
	}
	body := m[0].Body
	for _, want := range []string{"Members:", "demo", "blocked", "4h", "PR #130", pending, "ch_w1", accepted} {
		if !strings.Contains(body, want) {
			t.Fatalf("tick lacks %q:\n%s", want, body)
		}
	}
	// The worker with an accepted change ended in the tick's window: it is
	// listed as ended, and not as unreviewed.
	if i := strings.Index(body, accepted); i < 0 || strings.Contains(body[i:min(len(body), i+120)], "ch_w2") {
		t.Fatalf("accepted worker is shown as unreviewed:\n%s", body)
	}
	if !strings.HasPrefix(body, "[agent-net] Loop: ") || !strings.Contains(body, "Your purpose is Ship the parser.") {
		t.Fatalf("tick shape changed:\n%s", body)
	}
}

// An old worker whose change was accepted is no news. One whose change was not
// accepted stays in every tick until someone reviews it.
func TestLoopTickKeepsAnUnreviewedWorkerUntilItIsAccepted(t *testing.T) {
	s, c, member, lead := loopLeadRig(t)
	w := memberWorker(t, s, member, "ch_w1", c.t.Add(time.Minute))
	// A message between ticks is progress, so the loop does not pause.
	tick := func(key string) {
		t.Helper()
		if _, _, err := send(s, member, lead, key, ""); err != nil {
			t.Fatal(err)
		}
		idleSince(t, s, c, lead, time.Minute)
		runLoops(t, s, c)
	}
	tick("k1")
	tick("k2")
	m := loopMessages(t, s)
	if len(m) != 2 || !strings.Contains(m[0].Body, w) || !strings.Contains(m[1].Body, w) || !strings.Contains(m[1].Body, "ch_w1") {
		t.Fatalf("messages = %+v", m)
	}
	acceptChange(t, s, "ch_w1")
	tick("k3")
	m = loopMessages(t, s)
	if len(m) != 3 || strings.Contains(m[2].Body, "Members:") || strings.Contains(m[2].Body, w) {
		t.Fatalf("third message = %+v", m)
	}
}

func TestLoopTickHasNoMembersPartWhenThereIsNothingToSay(t *testing.T) {
	s, c, member, lead := loopLeadRig(t)
	announce(t, s, member, protocol.ProfileAnnounce{Task: "writing the tests"})
	idleSince(t, s, c, lead, time.Minute)
	runLoops(t, s, c)
	m := loopMessages(t, s)
	if len(m) != 1 || strings.Contains(m[0].Body, "Members:") {
		t.Fatalf("messages = %+v", m)
	}
}

// A team that sends questions to the owner is not this lead's to watch.
func TestLoopTickIgnoresMembersOfATeamThatAsksTheOwner(t *testing.T) {
	s, c, member, lead := loopLeadRig(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToOwner})
	announce(t, s, member, protocol.ProfileAnnounce{Task: deniedMerge})
	idleSince(t, s, c, lead, time.Minute)
	runLoops(t, s, c)
	m := loopMessages(t, s)
	if len(m) != 1 || strings.Contains(m[0].Body, "Members:") {
		t.Fatalf("messages = %+v", m)
	}
}

func TestLoopOnChangeCountsABlockedMemberAsAChange(t *testing.T) {
	s, c, member, lead := loopLeadRig(t)
	if err := s.SetNodeSetting("proj", protocol.SettingLoopMode, "on_change", "owner"); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, lead, time.Minute)
	if res := runLoops(t, s, c); len(res) != 1 || res[0].Outcome != protocol.LoopSkippedQuiet {
		t.Fatalf("quiet tick = %+v", res)
	}
	announce(t, s, member, protocol.ProfileAnnounce{Task: deniedMerge})
	idleSince(t, s, c, lead, time.Minute)
	res := runLoops(t, s, c)
	if len(res) != 1 || res[0].Outcome != protocol.LoopSent {
		t.Fatalf("tick with a blocked member = %+v", res)
	}
	m := loopMessages(t, s)
	if len(m) != 1 || !strings.Contains(m[0].Body, "Members:") || !strings.Contains(m[0].Body, "PR #130") {
		t.Fatalf("messages = %+v", m)
	}
}

func TestLoopTickShowsAMemberWhoseExplicitWaitIsSet(t *testing.T) {
	s, c, member, lead := loopLeadRig(t)
	announce(t, s, member, protocol.ProfileAnnounce{Task: "T2b worker running"})
	if _, err := s.db.Exec(`UPDATE sessions SET waiting=? WHERE id=?`,
		`{"explicit":true,"kind":"input","text":"need a decision on the schema","since":`+fmt.Sprint(c.t.Unix())+`}`, member); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, lead, time.Minute)
	runLoops(t, s, c)
	m := loopMessages(t, s)
	if len(m) != 1 || !strings.Contains(m[0].Body, "Members:") || !strings.Contains(m[0].Body, "need a decision on the schema") {
		t.Fatalf("messages = %+v", m)
	}
}
