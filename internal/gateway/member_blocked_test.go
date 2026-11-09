package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A member of a team that sends questions to its lead can be blocked without
// asking anything: it only announces "blocked on the owner". The lead hears
// of it from the gateway (decision 0111).

func blockedNotices(t *testing.T, s *Store) []protocol.Message {
	t.Helper()
	rows, err := s.db.Query(`SELECT id,sender,recipient,type,body,idem_key FROM messages WHERE idem_key LIKE 'member_blocked:%' ORDER BY created_at,rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []protocol.Message
	for rows.Next() {
		var m protocol.Message
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Type, &m.Body, &m.IdempotencyKey); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func announce(t *testing.T, s *Store, session string, p protocol.ProfileAnnounce) {
	t.Helper()
	node, err := s.SessionNode(session)
	if err != nil {
		t.Fatal(err)
	}
	p.SessionID = session
	if _, err := s.Announce(node, p); err != nil {
		t.Fatal(err)
	}
}

func leadTeamRig(t *testing.T) (s *Store, c *clock, member, lead string) {
	t.Helper()
	s, member, lead = forgeRig(t)
	c = &clock{t: time.Date(2026, 11, 9, 0, 10, 0, 0, time.UTC)}
	s.now = c.now
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToLead})
	return s, c, member, lead
}

const deniedMerge = "blocked on the owner: PR #130 is ready, merge was denied in this session"

func TestBlockedMemberAnnounceSendsTheLeadOneMessage(t *testing.T) {
	s, c, member, lead := leadTeamRig(t)
	announce(t, s, member, protocol.ProfileAnnounce{Task: deniedMerge})
	got := blockedNotices(t, s)
	if len(got) != 1 {
		t.Fatalf("notices after the first announce = %+v", got)
	}
	m := got[0]
	if m.From != "agent-net" || m.To != lead || m.Type != protocol.TypeNotification {
		t.Fatalf("notice = %+v", m)
	}
	for _, want := range []string{"demo", "forge", deniedMerge, "00:10 UTC"} {
		if !strings.Contains(m.Body, want) {
			t.Fatalf("notice lacks %q: %q", want, m.Body)
		}
	}
	// The same text again, later, sends nothing.
	c.t = c.t.Add(time.Hour)
	announce(t, s, member, protocol.ProfileAnnounce{Task: deniedMerge})
	announce(t, s, member, protocol.ProfileAnnounce{Task: "  " + deniedMerge + "  "})
	if got := blockedNotices(t, s); len(got) != 1 {
		t.Fatalf("a repeat announce sent a message: %+v", got)
	}
	// A changed blocked task tells the lead again.
	announce(t, s, member, protocol.ProfileAnnounce{Task: "blocked on CI: the runner is down"})
	if got := blockedNotices(t, s); len(got) != 2 || !strings.Contains(got[1].Body, "the runner is down") {
		t.Fatalf("notices after a new text = %+v", got)
	}
}

func TestMemberTaskThatIsNotBlockedSendsNothing(t *testing.T) {
	s, _, member, _ := leadTeamRig(t)
	for _, task := range []string{
		"T2b worker running", "done", "more: writing the tests",
		"unblocked, merging PR #130", "not blocked on anything now", "no longer waiting on the owner",
	} {
		announce(t, s, member, protocol.ProfileAnnounce{Task: task})
	}
	if got := blockedNotices(t, s); len(got) != 0 {
		t.Fatalf("notices = %+v", got)
	}
}

func TestBlockedPhrasesAndTheWaitingFieldAllCount(t *testing.T) {
	s, _, member, _ := leadTeamRig(t)
	announce(t, s, member, protocol.ProfileAnnounce{Task: "PR #130 ready; waiting on owner to merge"})
	announce(t, s, member, protocol.ProfileAnnounce{Task: "Blocked: no merge rights"})
	announce(t, s, member, protocol.ProfileAnnounce{Task: "T2b worker running", Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: "need a decision on the schema"}})
	got := blockedNotices(t, s)
	if len(got) != 3 {
		t.Fatalf("notices = %+v", got)
	}
	if !strings.Contains(got[2].Body, "need a decision on the schema") {
		t.Fatalf("the wait text is not in the notice: %q", got[2].Body)
	}
}

// When the member works again, nothing is sent. A later block of the same text
// is a new block and tells the lead again.
func TestMemberThatWorksAgainClearsTheBlock(t *testing.T) {
	s, _, member, _ := leadTeamRig(t)
	announce(t, s, member, protocol.ProfileAnnounce{Task: deniedMerge})
	announce(t, s, member, protocol.ProfileAnnounce{Task: "merged PR #130, starting T3"})
	if got := blockedNotices(t, s); len(got) != 1 {
		t.Fatalf("notices after it moved on = %+v", got)
	}
	announce(t, s, member, protocol.ProfileAnnounce{Task: deniedMerge})
	if got := blockedNotices(t, s); len(got) != 2 {
		t.Fatalf("notices after a second block = %+v", got)
	}
}

func TestBlockedMemberWithNoLeadSessionWritesOneDeskLine(t *testing.T) {
	s, _, member, lead := leadTeamRig(t)
	if _, err := s.db.Exec(`UPDATE sessions SET online=0,archived_at=1 WHERE id=?`, lead); err != nil {
		t.Fatal(err)
	}
	announce(t, s, member, protocol.ProfileAnnounce{Task: deniedMerge})
	announce(t, s, member, protocol.ProfileAnnounce{Task: deniedMerge})
	if got := blockedNotices(t, s); len(got) != 0 {
		t.Fatalf("notices = %+v", got)
	}
	var n int
	for _, l := range deskLines(t, s) {
		if strings.Contains(l, "no lead session") && strings.Contains(l, "forge") && strings.Contains(l, "PR #130") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("desk lines = %v", deskLines(t, s))
	}
}

func TestBlockedAnnounceOutsideALeadTeamSendsNothing(t *testing.T) {
	// The default team sends questions to the owner.
	s, member, lead := forgeRig(t)
	announce(t, s, member, protocol.ProfileAnnounce{Task: deniedMerge})
	if got := blockedNotices(t, s); len(got) != 0 {
		t.Fatalf("owner team: %+v", got)
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToLead})
	// The lead itself, and a session outside the team, tell nobody.
	announce(t, s, lead, protocol.ProfileAnnounce{Task: deniedMerge})
	ids, _ := graphSessions(t, s, "outside")
	announce(t, s, ids["outside"], protocol.ProfileAnnounce{Task: deniedMerge})
	if got := blockedNotices(t, s); len(got) != 0 {
		t.Fatalf("lead or outsider: %+v", got)
	}
	if lines := deskLines(t, s); hasLine(lines, "blocked") {
		t.Fatalf("desk lines = %v", lines)
	}
}

// runningWorker starts a worker that member owns and that is still running.
func runningWorker(t *testing.T, s *Store, c *clock, member string) {
	t.Helper()
	node := mustNode(t, s, member)
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "rework-worker", Name: "rework",
		Status: "idle", LastActive: c.t.Unix(),
		Worker: &protocol.WorkerInfo{ID: "w-rework", Owner: member, StartedAt: c.t.Unix()}}); err != nil {
		t.Fatal(err)
	}
}

// A progress answer that only says it waits on the member's own running worker
// is normal progress, so the lead hears nothing.
func TestMemberWaitingOnItsOwnRunningWorkerSendsNothing(t *testing.T) {
	s, c, member, _ := leadTeamRig(t)
	runningWorker(t, s, c, member)
	announce(t, s, member, protocol.ProfileAnnounce{Task: "more: waiting on worker rework of PR #79 ... then merge"})
	if got := blockedNotices(t, s); len(got) != 0 {
		t.Fatalf("notices for a wait on its own worker = %+v", got)
	}
}

// The waits that still reach the lead with a running worker: an explicit
// blocked answer, a wait the member set in the waiting field, and a phrase that
// is not a progress answer.
func TestMemberWaitingOnSomeoneElseWithARunningWorkerStillNotifies(t *testing.T) {
	s, c, member, _ := leadTeamRig(t)
	runningWorker(t, s, c, member)
	announce(t, s, member, protocol.ProfileAnnounce{Task: "blocked on the owner: PR #79 needs a merge right"})
	announce(t, s, member, protocol.ProfileAnnounce{Task: "more: rework of PR #79", Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: "need a decision on the schema"}})
	announce(t, s, member, protocol.ProfileAnnounce{Task: "waiting on owner to merge PR #79"})
	if got := blockedNotices(t, s); len(got) != 3 {
		t.Fatalf("notices = %+v", got)
	}
}

// A member with no running worker still tells the lead about a phrase match.
func TestMemberWaitingOnWorkerWithNoRunningWorkerStillNotifies(t *testing.T) {
	s, _, member, _ := leadTeamRig(t)
	announce(t, s, member, protocol.ProfileAnnounce{Task: "more: waiting on worker rework of PR #79 ... then merge"})
	if got := blockedNotices(t, s); len(got) != 1 {
		t.Fatalf("notices = %+v", got)
	}
}
