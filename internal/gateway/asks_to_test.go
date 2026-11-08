package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// forgeRig makes team forge with lead agent "other" and one member repo
// "demo". It returns the asker (a session of repo demo) and the lead session.
func forgeRig(t *testing.T) (s *Store, asker, lead string) {
	t.Helper()
	s = teamStore(t)
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='demo'`).Scan(&asker); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='other'`).Scan(&lead); err != nil {
		t.Fatal(err)
	}
	agent := protocol.GraphRef{Kind: "agent", ID: "other"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "forge", Name: "forge", Owner: &agent})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "forge", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})
	return s, asker, lead
}

func ownerAsk(session, text string) protocol.InboxItem {
	return protocol.InboxItem{Kind: "question", SessionID: session, Node: "demo", Text: text, Recommendation: "yes", Options: []string{"yes", "no"}, AskerName: "demo agent"}
}

func TestTeamAsksToDefaultsToOwnerAndTheOwnerAndCTOSetIt(t *testing.T) {
	s, _, _ := forgeRig(t)
	teams, err := s.Teams()
	if err != nil || len(teams) != 1 || teams[0].AsksTo != protocol.AsksToOwner {
		t.Fatalf("default: %+v %v", teams, err)
	}
	if err := s.WriteTeam(protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: "nobody"}, "owner"); err == nil {
		t.Fatal("invalid asks_to accepted")
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToLead})
	teams, _ = s.Teams()
	if teams[0].AsksTo != protocol.AsksToLead {
		t.Fatalf("owner write: %+v", teams)
	}
	// The CTO sets it through the team charter, with no proposal, and can undo it.
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "team", Node: "forge", Attr: "asks_to", Value: "owner"}, "cto", false); err != nil {
		t.Fatal(err)
	}
	teams, _ = s.Teams()
	if teams[0].AsksTo != protocol.AsksToOwner {
		t.Fatalf("cto write: %+v", teams)
	}
	changes, err := s.CTOGraphHistory(0)
	if err != nil || len(changes) != 1 {
		t.Fatalf("graph history: %+v %v", changes, err)
	}
	if err := s.GraphUndo(changes[0].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	teams, _ = s.Teams()
	if teams[0].AsksTo != protocol.AsksToLead {
		t.Fatalf("undo: %+v", teams)
	}
}

func TestOwnerAskGoesToTheTeamLeadWhenTheTeamAsksToLead(t *testing.T) {
	s, asker, lead := forgeRig(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToLead})
	red, msg, err := s.RedirectOwnerAsk(ownerAsk(asker, "Merge the forge rules?"))
	if err != nil || red == nil {
		t.Fatalf("redirect: %+v %v", red, err)
	}
	if red.Team != "forge" || red.Lead != "other" || red.LeadSession != lead {
		t.Fatalf("redirect: %+v", red)
	}
	want := "Your team (forge) sends questions to its lead, other. Sent there; carry on with work that does not need the answer."
	if red.Note != want {
		t.Fatalf("note = %q, want %q", red.Note, want)
	}
	// The lead gets a request from the asker with the question.
	if msg.From != asker || msg.To != lead || msg.Type != protocol.TypeRequest ||
		!strings.Contains(msg.Body, "Merge the forge rules?") || !strings.Contains(msg.Body, "yes") {
		t.Fatalf("message: %+v", msg)
	}
	// The Desk has one ask, with the lead as the party that acts now. The
	// owner's old Inbox does not list it.
	asks, err := s.ListAsks()
	if err != nil || len(asks) != 1 {
		t.Fatalf("asks: %+v %v", asks, err)
	}
	a := asks[0]
	if a.Next != lead || a.State != protocol.AskWithSomeone || a.FromSession != asker || a.MessageID != msg.ID || a.Question != "Merge the forge rules?" {
		t.Fatalf("ask: %+v", a)
	}
	if inbox, err := s.ListInbox(); err != nil || len(inbox) != 0 {
		t.Fatalf("owner inbox: %+v %v", inbox, err)
	}
	// The lead answers with the normal reply path; the ask closes and the
	// answer reaches the asker.
	reply, created, err := s.StoreMessage(protocol.MsgSend{Sender: lead, Recipient: asker, ParentID: msg.ID, Body: "yes, merge"})
	if err != nil || !created || reply.To != asker {
		t.Fatalf("reply: %+v %v", reply, err)
	}
	if asks, _ = s.ListAsks(); len(asks) != 0 {
		t.Fatalf("ask still open: %+v", asks)
	}
	status, answer, _ := askRow(t, s, `id=?`, a.ID)
	if status != "answered" || answer != "yes, merge" {
		t.Fatalf("ask row: %q %q", status, answer)
	}
}

func TestOwnerAskStaysWithTheOwnerByDefault(t *testing.T) {
	s, asker, _ := forgeRig(t)
	red, _, err := s.RedirectOwnerAsk(ownerAsk(asker, "Merge?"))
	if err != nil || red != nil {
		t.Fatalf("default redirected: %+v %v", red, err)
	}
}

func TestLeadsOwnAskIsNotRedirected(t *testing.T) {
	s, _, lead := forgeRig(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToLead})
	red, _, err := s.RedirectOwnerAsk(ownerAsk(lead, "Spend money?"))
	if err != nil || red != nil {
		t.Fatalf("lead redirected: %+v %v", red, err)
	}
}

func TestAskOutsideATeamIsNotRedirected(t *testing.T) {
	s, _, _ := forgeRig(t)
	ids, _ := graphSessions(t, s, "outside")
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToLead})
	red, _, err := s.RedirectOwnerAsk(ownerAsk(ids["outside"], "Merge?"))
	if err != nil || red != nil {
		t.Fatalf("outsider redirected: %+v %v", red, err)
	}
}

// The tool has no text classifier for the owner's held back items, so every
// ask of such a team goes to its lead. The message tells the lead what stays
// with the owner.
func TestRedirectedAskTellsTheLeadWhatStaysWithTheOwner(t *testing.T) {
	s, asker, _ := forgeRig(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToLead})
	_, msg, err := s.RedirectOwnerAsk(ownerAsk(asker, "Delete the production database?"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.Body, protocol.AlwaysHeldBack) || !strings.Contains(msg.Body, "agent_owner_ask") {
		t.Fatalf("body lacks the held back line: %q", msg.Body)
	}
}

// A new ask of the same kind replaces the earlier open one, as for the owner.
func TestRedirectedAskReplacesTheEarlierOneOfTheSameKind(t *testing.T) {
	s, asker, _ := forgeRig(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToLead})
	if _, _, err := s.RedirectOwnerAsk(ownerAsk(asker, "first?")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RedirectOwnerAsk(ownerAsk(asker, "second?")); err != nil {
		t.Fatal(err)
	}
	asks, _ := s.ListAsks()
	if len(asks) != 1 || asks[0].Question != "second?" {
		t.Fatalf("asks: %+v", asks)
	}
}

// A lead Agent with no session at all cannot take the question: it goes to the
// owner, as before. A lead with only an offline session still gets the message.
func TestAskGoesToTheOwnerWhenTheLeadHasNoSession(t *testing.T) {
	s, asker, lead := forgeRig(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToLead})
	if _, err := s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, lead); err != nil {
		t.Fatal(err)
	}
	red, msg, err := s.RedirectOwnerAsk(ownerAsk(asker, "offline lead?"))
	if err != nil || red == nil || msg.To != lead {
		t.Fatalf("offline lead: %+v %+v %v", red, msg, err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET archived_at=1 WHERE id=?`, lead); err != nil {
		t.Fatal(err)
	}
	red, _, err = s.RedirectOwnerAsk(ownerAsk(asker, "no lead session?"))
	if err != nil || red != nil {
		t.Fatalf("lead with no session: %+v %v", red, err)
	}
}

// Team rules reach every member, also one with its own repo rules.
func TestRulesUnionAcrossLevels(t *testing.T) {
	s := teamStore(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "forge", Name: "forge"})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "forge", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})
	for _, q := range []protocol.CharterSetRequest{
		{Level: "team", Node: "forge", Attr: "rules", Value: "Merge only on green CI; Ask the lead first"},
		{Level: "repo", Node: "demo", Attr: "rules", Value: "Use plain English\nAsk the lead first"},
		{Level: "agent", Node: "demo", Attr: "rules", Value: "Keep commits small"},
	} {
		if err := s.SetLevelCharter(q, "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	c, err := s.EffectiveCharter("demo", "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	want := "Merge only on green CI; Ask the lead first; Use plain English; Keep commits small"
	if c.Rules != want {
		t.Fatalf("rules = %q, want %q", c.Rules, want)
	}
	if c.Sources == nil || len((*c.Sources)["rules"]) != 3 {
		t.Fatalf("rule sources: %+v", c.Sources)
	}
	// A repo with no rules of its own still gets the team rules.
	other, err := s.EffectiveCharter("other", "", "forge")
	if err != nil || !strings.Contains(other.Rules, "Merge only on green CI") {
		t.Fatalf("team context: %+v %v", other, err)
	}
}
