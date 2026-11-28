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

func TestMemberWaitingOnItsLeadSendsOneMessageToTheLead(t *testing.T) {
	for _, asksTo := range []string{protocol.AsksToLead, protocol.AsksToOwner} {
		t.Run("team asks "+asksTo, func(t *testing.T) {
			s, c, member, lead := leadTeamRig(t)
			teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: asksTo})
			const text = "other confirming the schema change"
			announce(t, s, member, protocol.ProfileAnnounce{Task: "T2b worker running", Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: text}})
			got := blockedNotices(t, s)
			if len(got) != 1 || got[0].To != lead || got[0].Type != protocol.TypeNotification || !strings.Contains(got[0].Body, text) {
				t.Fatalf("messages to the lead = %+v", got)
			}
			// The same wait again sends nothing more.
			c.advance(time.Minute)
			announce(t, s, member, protocol.ProfileAnnounce{Task: "T2b worker running", Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: text}})
			if got := blockedNotices(t, s); len(got) != 1 {
				t.Fatalf("a repeat wait: %d messages", len(got))
			}
		})
	}
}

func TestMemberWaitingOnItsRepoLeadSendsNoticeToRepoLead(t *testing.T) {
	s := teamStore(t)
	var member, lead, leadAgent string
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='demo'`).Scan(&member); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id,agent_id FROM sessions WHERE repo_id='other'`).Scan(&lead, &leadAgent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE repos SET lead_agent_id=? WHERE id='demo'`, leadAgent); err != nil {
		t.Fatal(err)
	}
	const text = "other confirming the plan"
	announce(t, s, member, protocol.ProfileAnnounce{Task: "T2b", Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: text}})
	if got := blockedNotices(t, s); len(got) != 1 || got[0].To != lead {
		t.Fatalf("messages to the repo lead = %+v", got)
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

// Names share prefixes: the lead "tenebrae" and the repo "tenebrae-strats". A
// hyphen joins a word, so a wait on the repo is not a wait on the lead. Dots,
// commas, colons and spaces still end a word.
func TestWaitNamesLeadOnlyAsAWholeHyphenatedName(t *testing.T) {
	s, _, member, lead := leadTeamRig(t)
	if _, err := s.db.Exec(`UPDATE agents SET display_name='tenebrae' WHERE id=(SELECT agent_id FROM sessions WHERE id=?)`, lead); err != nil {
		t.Fatal(err)
	}
	for text, want := range map[string]bool{
		"waiting: tenebrae-strats deploy":          false,
		"waiting: ingest-tenebrae deploy":          false,
		"waiting: tenebrae to confirm.":            true,
		"waiting: tenebrae, then the deploy":       true,
		"waiting: tenebrae: confirm the schema":    true,
		"waiting: for tenebrae":                    true,
		"waiting: (tenebrae) to confirm":           true,
		"waiting: tenebrae-strats and tenebrae ok": true,
	} {
		_, got, err := s.waitNamesLead(s.db, member, text)
		if err != nil || got != want {
			t.Errorf("waitNamesLead(%q) = %v %v, want %v", text, got, err, want)
		}
	}
}
