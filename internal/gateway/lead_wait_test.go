package gateway

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A member that waits on its own lead is in an ordinary wait inside its team.
// The owner has nothing to do with it (decision 0126).

func openWaitingAsks(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE kind='waiting' AND status='open'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// reportWait stores the wait of a session the way a session report does and runs
// the ask sync that report runs.
func reportWait(t *testing.T, s *Store, session, text string, now time.Time) {
	t.Helper()
	w := fmt.Sprintf(`{"kind":"input","text":%q,"since":%d}`, text, now.Unix())
	if _, err := s.db.Exec(`UPDATE sessions SET waiting=? WHERE id=?`, w, session); err != nil {
		t.Fatal(err)
	}
	if err := s.syncWaitingAsk(s.db, session, w); err != nil {
		t.Fatal(err)
	}
}

func TestMemberWaitingOnItsLeadMakesNoOwnerAskAndOneMessageToTheLead(t *testing.T) {
	for _, asksTo := range []string{protocol.AsksToLead, protocol.AsksToOwner} {
		t.Run("team asks "+asksTo, func(t *testing.T) {
			s, c, member, lead := leadTeamRig(t)
			teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: asksTo})
			const text = "other confirming the schema change"
			announce(t, s, member, protocol.ProfileAnnounce{Task: "T2b worker running", Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: text}})
			reportWait(t, s, member, text, c.t)
			if n := openWaitingAsks(t, s); n != 0 {
				t.Fatalf("a wait on the lead made %d owner asks", n)
			}
			got := blockedNotices(t, s)
			if len(got) != 1 || got[0].To != lead || got[0].Type != protocol.TypeNotification || !strings.Contains(got[0].Body, text) {
				t.Fatalf("messages to the lead = %+v", got)
			}
			// The same wait again sends nothing more.
			c.t = c.t.Add(time.Minute)
			announce(t, s, member, protocol.ProfileAnnounce{Task: "T2b worker running", Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: text}})
			reportWait(t, s, member, text, c.t)
			if got := blockedNotices(t, s); len(got) != 1 || openWaitingAsks(t, s) != 0 {
				t.Fatalf("a repeat wait: %d messages, %d asks", len(got), openWaitingAsks(t, s))
			}
		})
	}
}

func TestMemberWaitingOnItsRepoLeadMakesNoOwnerAsk(t *testing.T) {
	s := teamStore(t)
	var member, lead, leadAgent string
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='demo'`).Scan(&member); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id,agent_id FROM sessions WHERE repo_id='other'`).Scan(&lead, &leadAgent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO repo_leads(repo_id,agent_id) VALUES('demo',?)`, leadAgent); err != nil {
		t.Fatal(err)
	}
	const text = "other confirming the plan"
	announce(t, s, member, protocol.ProfileAnnounce{Task: "T2b", Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: text}})
	reportWait(t, s, member, text, s.now())
	if n := openWaitingAsks(t, s); n != 0 {
		t.Fatalf("a wait on the repo lead made %d owner asks", n)
	}
	if got := blockedNotices(t, s); len(got) != 1 || got[0].To != lead {
		t.Fatalf("messages to the repo lead = %+v", got)
	}
}

// Waits that name the owner, or nobody, keep the path they had.
func TestMemberWaitThatNamesTheOwnerOrNobodyStillMakesAnAsk(t *testing.T) {
	for _, text := range []string{"the owner choosing the database", "which database?", "other and the owner agreeing on the plan"} {
		t.Run(text, func(t *testing.T) {
			s, c, member, _ := leadTeamRig(t)
			reportWait(t, s, member, text, c.t)
			if n := openWaitingAsks(t, s); n != 1 {
				t.Fatalf("owner asks = %d, want 1", n)
			}
		})
	}
}

// A reply to a message of agent-net about an ask answers the ask. Before,
// agent_message_reply failed with: unknown recipient session "agent-net".
func TestReplyToAnAgentNetAskMessageClosesTheAsk(t *testing.T) {
	s, _, chief, asker, node := chiefRig(t)
	other, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "nc", Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	send := func(kind, text string) (ask string, msg protocol.Message) {
		t.Helper()
		ask = chiefAskKind(t, s, asker, kind, text)
		_, msg, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{Ask: ask, Result: protocol.AskSortSend, To: other, Reason: "carol runs it"}})
		if err != nil {
			t.Fatal(err)
		}
		if msg.From != "agent-net" || msg.To != other {
			t.Fatalf("sent message = %+v", msg)
		}
		return ask, msg
	}

	// A body answers the ask: it closes with the reply as the reason.
	ask, msg := send("question", "Who owns the cache?")
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: other, Recipient: "agent-net", ParentID: msg.ID, Body: "Carol owns it; use the shared one"}); err != nil {
		t.Fatalf("reply to agent-net: %v", err)
	}
	status, answer, _ := askRow(t, s, `id=?`, ask)
	if status != "closed" || !strings.Contains(answer, "Carol owns it; use the shared one") {
		t.Fatalf("ask after a reply: %q %q", status, answer)
	}

	// An ack closes it too.
	ask, msg = send("feature_idea", "Add a cache?")
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: other, Recipient: "agent-net", ParentID: msg.ID, Ack: true}); err != nil {
		t.Fatalf("ack to agent-net: %v", err)
	}
	if status, _, _ := askRow(t, s, `id=?`, ask); status != "closed" {
		t.Fatalf("ask after an ack: %q", status)
	}

	// Only the session the message went to may answer it.
	_, msg = send("approval", "Merge PR 9?")
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: asker, Recipient: "agent-net", ParentID: msg.ID, Body: "I say yes"}); err == nil {
		t.Fatal("a session that did not get the message answered it")
	}

	// A decision only the owner can make stays open, with a clear error.
	ask, msg = send("approval", "Pay for the new model?")
	if _, err := s.db.Exec(`UPDATE inbox_items SET route_fixed=1 WHERE id=?`, ask); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: other, Recipient: "agent-net", ParentID: msg.ID, Body: "yes"})
	if err == nil || !strings.Contains(err.Error(), "only the owner") {
		t.Fatalf("reply to an owner-only ask: %v", err)
	}
	if status, _, _ := askRow(t, s, `id=?`, ask); status != "open" {
		t.Fatalf("an owner-only ask changed to %q", status)
	}
}

// One wait, several ticks: the lead hears of it once. A changed text is new.
func TestUnchangedWaitGivesOneNoticeOverSeveralTicks(t *testing.T) {
	s, c, member, lead := loopLeadRig(t)
	announce(t, s, member, protocol.ProfileAnnounce{Task: "T2b worker running"})
	explicit := func(text string) {
		t.Helper()
		w := fmt.Sprintf(`{"explicit":true,"kind":"input","text":%q,"since":%d}`, text, c.t.Unix())
		if _, err := s.db.Exec(`UPDATE sessions SET waiting=? WHERE id=?`, w, member); err != nil {
			t.Fatal(err)
		}
	}
	tickBodies := func() []string {
		t.Helper()
		var out []string
		// The lead reports progress, so the loop does not pause for none.
		announce(t, s, lead, protocol.ProfileAnnounce{Task: "working on the parser"})
		for i := 0; i < 2; i++ {
			idleSince(t, s, c, lead, time.Minute)
			runLoops(t, s, c)
		}
		for _, m := range loopMessages(t, s) {
			out = append(out, m.Body)
		}
		return out
	}
	explicit("need a decision on the schema")
	bodies := tickBodies()
	if len(bodies) != 2 {
		t.Fatalf("ticks sent = %d", len(bodies))
	}
	for i, b := range bodies {
		if has := strings.Contains(b, "need a decision on the schema"); has != (i == 0) {
			t.Fatalf("tick %d: wait listed = %v\n%s", i, has, b)
		}
	}
	// The wait text changes: it is listed once more.
	explicit("need a decision on the index")
	bodies = tickBodies()
	if len(bodies) != 4 {
		t.Fatalf("ticks sent = %d", len(bodies))
	}
	for i, b := range bodies[2:] {
		if has := strings.Contains(b, "need a decision on the index"); has != (i == 0) {
			t.Fatalf("tick %d after the change: wait listed = %v\n%s", i, has, b)
		}
	}
}

// The announce notice follows the wait text, not the task line around it.
func TestAnnounceNoticeFollowsTheWaitTextNotTheTask(t *testing.T) {
	s, _, member, _ := leadTeamRig(t)
	w := &protocol.Waiting{Kind: protocol.WaitingInput, Text: "need a decision on the schema"}
	for _, task := range []string{"T2b worker running", "T2b worker still running", "T2b worker running, 3 tests left"} {
		announce(t, s, member, protocol.ProfileAnnounce{Task: task, Waiting: w})
	}
	if got := blockedNotices(t, s); len(got) != 1 {
		t.Fatalf("notices for one unchanged wait = %d: %+v", len(got), got)
	}
	announce(t, s, member, protocol.ProfileAnnounce{Task: "T2b worker running", Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: "need a decision on the index"}})
	if got := blockedNotices(t, s); len(got) != 2 {
		t.Fatalf("notices after a changed wait = %d", len(got))
	}
}
